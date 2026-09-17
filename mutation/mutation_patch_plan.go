package mutation

import (
	"bytes"
	"context"
	"crypto/sha256"
	"encoding/binary"
	"errors"
	"hash"
	"io"
	"unicode/utf8"

	"github.com/skosovsky/okf/bundle"
	"gopkg.in/yaml.v3"
)

type bytePatch struct {
	Start, End int
	Text       []byte
	edit       *semanticEdit
	Owner      *yaml.Node
}

// semanticEdit is the private semantic contract carried by a YAML byte patch. Offsets
// alone are not a contract: a valid YAML token at the wrong node is still a
// corrupt mutation.
type semanticEdit struct {
	path          []int
	before, after string
	expected      *yamlSemanticNode
}

type yamlSemanticNode struct {
	Kind    yaml.Kind
	Tag     string
	Value   string
	Anchor  string
	Alias   bool
	Content []*yamlSemanticNode
}

type auditedBytePatches struct {
	patches           []auditedBytePatch
	outputLen         int
	sourceLen         int
	sourceFingerprint [sha256.Size]byte
}

// auditedBytePatch is deliberately distinct from bytePatch. An audit freezes
// every caller-owned field before a plan can cross the audit/apply boundary.
type auditedBytePatch struct {
	Start, End int
	Text       []byte
	edit       *semanticEdit
	owner      *auditedPatchOwner
	ordinal    int
}

type auditedPatchOwner struct {
	path        []int
	span        SourceSpan
	kind        yaml.Kind
	tag         string
	value       string
	anchor      string
	contentKind []yaml.Kind
	fingerprint [sha256.Size]byte
}

func invalidPatchSpan(sourceLen, start, end int) SourceSpan {
	if sourceLen <= 0 {
		return SourceSpan{}
	}
	at := start
	if at < 0 {
		at = 0
	}
	if at >= sourceLen {
		at = sourceLen - 1
	}
	if end > start && end <= sourceLen {
		return SourceSpan{Start: at, End: max(at+1, end)}
	}
	return SourceSpan{Start: at, End: at + 1}
}

func invalidBytePatchError(sourceLen int, patch bytePatch) error {
	return yamlPresentationError("invalid_patch", ErrUnsupportedPresentation, invalidPatchSpan(sourceLen, patch.Start, patch.End))
}

func auditBytePatchesContext(ctx context.Context, source []byte, input []bytePatch) (auditedBytePatches, error) {
	if ctx == nil {
		return auditedBytePatches{}, yamlPresentationError("invalid_patch", ErrUnsupportedPresentation, SourceSpan{})
	}
	if err := ctx.Err(); err != nil {
		return auditedBytePatches{}, err
	}
	sourceLen := len(source)
	_, validUTF8, err := validateUTF8Context(ctx, source)
	if err != nil {
		return auditedBytePatches{}, err
	}
	if !validUTF8 {
		return auditedBytePatches{}, yamlPresentationError("invalid_patch", ErrUnsupportedPresentation, invalidPatchSpan(sourceLen, 0, sourceLen))
	}
	patches := make([]auditedBytePatch, len(input))
	for index := range input {
		if index&4095 == 0 {
			if err := ctx.Err(); err != nil {
				return auditedBytePatches{}, err
			}
		}
		patch := input[index]
		if patch.Start < 0 || patch.End < patch.Start || patch.End > sourceLen ||
			patch.Start < sourceLen && !utf8.RuneStart(source[patch.Start]) ||
			patch.End < sourceLen && !utf8.RuneStart(source[patch.End]) {
			return auditedBytePatches{}, invalidBytePatchError(sourceLen, patch)
		}
		ownedText, err := appendBytesContext(ctx, nil, patch.Text)
		if err != nil {
			return auditedBytePatches{}, err
		}
		ownedEdit, err := cloneSemanticEditContext(ctx, patch.edit)
		if err != nil {
			if errors.Is(err, errInvalidSemanticEdit) {
				return auditedBytePatches{}, invalidBytePatchError(sourceLen, patch)
			}
			return auditedBytePatches{}, err
		}
		patches[index] = auditedBytePatch{
			Start: patch.Start, End: patch.End, Text: ownedText, edit: ownedEdit, ordinal: index,
		}
	}
	if err := sortSliceContext(ctx, patches, func(left, right auditedBytePatch) bool {
		if left.Start != right.Start {
			return left.Start < right.Start
		}
		if left.End != right.End {
			return left.End < right.End
		}
		return left.ordinal < right.ordinal
	}); err != nil {
		return auditedBytePatches{}, err
	}
	outputLen := sourceLen
	lastEnd := 0
	for index := range patches {
		if index&4095 == 0 {
			if err := ctx.Err(); err != nil {
				return auditedBytePatches{}, err
			}
		}
		patch := patches[index]
		// Insertions are allowed at a replaced range boundary and retain caller
		// order at the same point. An insertion in the interior is overlapping.
		if patch.Start < lastEnd {
			return auditedBytePatches{}, yamlPresentationError(yamlCodeOverlappingPatch, ErrUnsupportedPresentation, unionSourceSpans(
				SourceSpan{Start: lastEnd - 1, End: lastEnd},
				SourceSpan{Start: patch.Start, End: max(patch.Start+1, patch.End)},
			))
		}
		removed := patch.End - patch.Start
		if removed > 0 {
			lastEnd = patch.End
		}
		if outputLen < removed {
			return auditedBytePatches{}, invalidBytePatchError(sourceLen, bytePatch{Start: patch.Start, End: patch.End})
		}
		outputLen -= removed
		if len(patch.Text) > int(^uint(0)>>1)-outputLen {
			return auditedBytePatches{}, invalidBytePatchError(sourceLen, bytePatch{Start: patch.Start, End: patch.End})
		}
		outputLen += len(patch.Text)
	}
	fingerprint, err := byteFingerprintContext(ctx, source)
	if err != nil {
		return auditedBytePatches{}, err
	}
	return auditedBytePatches{
		patches: patches, outputLen: outputLen, sourceLen: sourceLen, sourceFingerprint: fingerprint,
	}, ctx.Err()
}

func (p *presentation) auditBytePatchesContext(ctx context.Context, input []bytePatch) (auditedBytePatches, error) {
	if p == nil {
		return auditedBytePatches{}, yamlPresentationError("invalid_patch", ErrUnsupportedPresentation, SourceSpan{})
	}
	plan, err := auditBytePatchesContext(ctx, p.data, input)
	if err != nil {
		return auditedBytePatches{}, err
	}
	ownerPatch, ownerErr := firstInputPatchOwnerContext(ctx, input)
	if ownerErr != nil {
		return auditedBytePatches{}, ownerErr
	}
	if ownerPatch >= 0 {
		patch := input[ownerPatch]
		if patch.Owner != nil {
			if err := validateMutationYAMLGraphContext(ctx, p.root); err != nil {
				if errors.Is(err, context.Canceled) || errors.Is(err, context.DeadlineExceeded) {
					return auditedBytePatches{}, err
				}
				return auditedBytePatches{}, invalidBytePatchError(len(p.data), patch)
			}
		}
	}
	for index := range plan.patches {
		if err := ctx.Err(); err != nil {
			return auditedBytePatches{}, err
		}
		patch := &plan.patches[index]
		original := input[patch.ordinal]
		if original.Owner != nil {
			proof, err := p.auditPatchOwnerContext(ctx, original.Owner)
			if err != nil {
				if errors.Is(err, context.Canceled) || errors.Is(err, context.DeadlineExceeded) {
					return auditedBytePatches{}, err
				}
				return auditedBytePatches{}, invalidBytePatchError(len(p.data), original)
			}
			patch.owner = proof
		}
		if patch.edit != nil && patch.edit.expected == nil {
			node, pathErr := nodeAtPathContext(ctx, p.root, patch.edit.path)
			if pathErr != nil {
				return auditedBytePatches{}, pathErr
			}
			if node == nil {
				return auditedBytePatches{}, invalidBytePatchError(len(p.data), original)
			}
		}
	}
	return plan, ctx.Err()
}

func firstInputPatchOwnerContext(ctx context.Context, input []bytePatch) (int, error) {
	for index := range input {
		if index&4095 == 0 {
			if err := ctx.Err(); err != nil {
				return -1, err
			}
		}
		if input[index].Owner != nil {
			return index, nil
		}
	}
	return -1, ctx.Err()
}

func cloneSemanticEditContext(ctx context.Context, edit *semanticEdit) (*semanticEdit, error) {
	if edit == nil {
		return nil, nil
	}
	if len(edit.path) > bundle.MaxYAMLPhysicalDepth {
		return nil, errInvalidSemanticEdit
	}
	path := make([]int, len(edit.path))
	for index, component := range edit.path {
		if err := ctx.Err(); err != nil {
			return nil, err
		}
		if component < 0 {
			return nil, errInvalidSemanticEdit
		}
		path[index] = component
	}
	expected, err := cloneSemanticNodeContext(ctx, edit.expected)
	if err != nil {
		return nil, err
	}
	return &semanticEdit{path: path, before: edit.before, after: edit.after, expected: expected}, ctx.Err()
}

func cloneSemanticNodeContext(ctx context.Context, node *yamlSemanticNode) (*yamlSemanticNode, error) {
	if err := ctx.Err(); err != nil {
		return nil, err
	}
	if node == nil {
		return nil, nil
	}
	clone := &yamlSemanticNode{Kind: node.Kind, Tag: node.Tag, Value: node.Value, Anchor: node.Anchor, Alias: node.Alias}
	type cloneFrame struct {
		source, target *yamlSemanticNode
		physicalDepth  int
	}
	depth := 0
	if node.Kind == yaml.MappingNode || node.Kind == yaml.SequenceNode {
		depth = 1
	}
	stack := []cloneFrame{{source: node, target: clone, physicalDepth: depth}}
	seen := make(map[*yamlSemanticNode]struct{})
	nodes, edges := 0, 0
	for len(stack) != 0 {
		if err := ctx.Err(); err != nil {
			return nil, err
		}
		frame := stack[len(stack)-1]
		stack = stack[:len(stack)-1]
		if _, duplicate := seen[frame.source]; duplicate {
			return nil, errInvalidSemanticEdit
		}
		seen[frame.source] = struct{}{}
		nodes++
		if nodes > bundle.MaxYAMLGraphNodes || frame.physicalDepth > bundle.MaxYAMLPhysicalDepth {
			return nil, errInvalidSemanticEdit
		}
		if len(frame.source.Content) > bundle.MaxYAMLGraphEdges-edges {
			return nil, errInvalidSemanticEdit
		}
		edges += len(frame.source.Content)
		if !validSemanticNodeShape(frame.source) {
			return nil, errInvalidSemanticEdit
		}
		frame.target.Content = make([]*yamlSemanticNode, len(frame.source.Content))
		for index := len(frame.source.Content) - 1; index >= 0; index-- {
			if (len(frame.source.Content)-1-index)&4095 == 0 {
				if err := ctx.Err(); err != nil {
					return nil, err
				}
			}
			child := frame.source.Content[index]
			if child == nil {
				return nil, errInvalidSemanticEdit
			}
			childClone := &yamlSemanticNode{Kind: child.Kind, Tag: child.Tag, Value: child.Value, Anchor: child.Anchor, Alias: child.Alias}
			frame.target.Content[index] = childClone
			childDepth := frame.physicalDepth
			if child.Kind == yaml.MappingNode || child.Kind == yaml.SequenceNode {
				childDepth++
			}
			stack = append(stack, cloneFrame{source: child, target: childClone, physicalDepth: childDepth})
		}
	}
	return clone, ctx.Err()
}

func validSemanticNodeShape(node *yamlSemanticNode) bool {
	if node == nil {
		return false
	}
	switch node.Kind {
	case yaml.DocumentNode:
		return len(node.Content) == 1 && !node.Alias
	case yaml.MappingNode:
		return len(node.Content)%2 == 0 && !node.Alias
	case yaml.SequenceNode:
		return !node.Alias
	case yaml.ScalarNode:
		return len(node.Content) == 0 && !node.Alias
	case yaml.AliasNode:
		return len(node.Content) == 0 && node.Alias
	default:
		return false
	}
}

func byteFingerprintContext(ctx context.Context, source []byte) ([sha256.Size]byte, error) {
	hasher := sha256.New()
	const chunkSize = 64 << 10
	for offset := 0; offset < len(source); offset += chunkSize {
		if err := ctx.Err(); err != nil {
			return [sha256.Size]byte{}, err
		}
		end := min(offset+chunkSize, len(source))
		_, _ = hasher.Write(source[offset:end])
	}
	var fingerprint [sha256.Size]byte
	copy(fingerprint[:], hasher.Sum(nil))
	return fingerprint, ctx.Err()
}

func stringFingerprintContext(ctx context.Context, source string) ([sha256.Size]byte, error) {
	hasher := sha256.New()
	const chunkSize = 64 << 10
	for offset := 0; offset < len(source); offset += chunkSize {
		if err := ctx.Err(); err != nil {
			return [sha256.Size]byte{}, err
		}
		end := min(offset+chunkSize, len(source))
		_, _ = io.WriteString(hasher, source[offset:end])
	}
	var fingerprint [sha256.Size]byte
	copy(fingerprint[:], hasher.Sum(nil))
	return fingerprint, ctx.Err()
}

func (plan auditedBytePatches) validateSourceContext(ctx context.Context, source []byte) error {
	if len(source) != plan.sourceLen {
		return yamlPresentationError("invalid_patch", ErrUnsupportedPresentation, invalidPatchSpan(len(source), 0, len(source)))
	}
	fingerprint, err := byteFingerprintContext(ctx, source)
	if err != nil {
		return err
	}
	if fingerprint != plan.sourceFingerprint {
		return yamlPresentationError("invalid_patch", ErrUnsupportedPresentation, invalidPatchSpan(len(source), 0, len(source)))
	}
	return nil
}

func (p *presentation) auditPatchOwnerContext(ctx context.Context, owner *yaml.Node) (*auditedPatchOwner, error) {
	path, owned := p.paths[owner]
	if !owned || len(path) > bundle.MaxYAMLPhysicalDepth {
		return nil, ErrUnsupportedPresentation
	}
	fingerprint, err := p.yamlNodeFingerprintContext(ctx, owner)
	if err != nil {
		return nil, err
	}
	ownedPath := make([]int, len(path))
	for index, component := range path {
		if err := ctx.Err(); err != nil {
			return nil, err
		}
		ownedPath[index] = component
	}
	proof := &auditedPatchOwner{
		path: ownedPath, span: p.yamlNodeSpan(owner), kind: owner.Kind,
		tag: owner.Tag, value: owner.Value, anchor: owner.Anchor, fingerprint: fingerprint,
		contentKind: nil,
	}
	proof.contentKind, err = cloneYAMLContentKindsContext(ctx, owner.Content)
	if err != nil {
		return nil, err
	}
	return proof, ctx.Err()
}

func cloneYAMLContentKindsContext(ctx context.Context, content []*yaml.Node) ([]yaml.Kind, error) {
	kinds := make([]yaml.Kind, len(content))
	for index, child := range content {
		if index&4095 == 0 {
			if err := ctx.Err(); err != nil {
				return nil, err
			}
		}
		if child == nil {
			return nil, errInvalidSemanticEdit
		}
		kinds[index] = child.Kind
	}
	return kinds, ctx.Err()
}

func (p *presentation) yamlNodeFingerprintContext(ctx context.Context, node *yaml.Node) ([sha256.Size]byte, error) {
	hasher := sha256.New()
	if err := p.writeYAMLNodeFingerprintContext(ctx, hasher, node); err != nil {
		return [sha256.Size]byte{}, err
	}
	var fingerprint [sha256.Size]byte
	copy(fingerprint[:], hasher.Sum(nil))
	return fingerprint, ctx.Err()
}

func (p *presentation) writeYAMLNodeFingerprintContext(ctx context.Context, destination hash.Hash, node *yaml.Node) error {
	stack := []*yaml.Node{node}
	seen := make(map[*yaml.Node]struct{})
	for len(stack) != 0 {
		if err := ctx.Err(); err != nil {
			return err
		}
		current := stack[len(stack)-1]
		stack = stack[:len(stack)-1]
		if current == nil {
			return errInvalidSemanticEdit
		}
		if _, duplicate := seen[current]; duplicate {
			return errInvalidSemanticEdit
		}
		seen[current] = struct{}{}
		var number [8]byte
		binary.BigEndian.PutUint64(number[:], uint64(current.Kind))
		if err := writeFramedFingerprintBytesContext(ctx, destination, number[:]); err != nil {
			return err
		}
		for _, value := range []string{current.Tag, current.Value, current.Anchor} {
			if err := writeFramedFingerprintStringContext(ctx, destination, value); err != nil {
				return err
			}
		}
		aliasPath := p.paths[current.Alias]
		binary.BigEndian.PutUint64(number[:], uint64(len(aliasPath)))
		if err := writeFramedFingerprintBytesContext(ctx, destination, number[:]); err != nil {
			return err
		}
		for _, component := range aliasPath {
			binary.BigEndian.PutUint64(number[:], uint64(component))
			if err := writeFramedFingerprintBytesContext(ctx, destination, number[:]); err != nil {
				return err
			}
		}
		binary.BigEndian.PutUint64(number[:], uint64(len(current.Content)))
		if err := writeFramedFingerprintBytesContext(ctx, destination, number[:]); err != nil {
			return err
		}
		for index := len(current.Content) - 1; index >= 0; index-- {
			if (len(current.Content)-1-index)&4095 == 0 {
				if err := ctx.Err(); err != nil {
					return err
				}
			}
			stack = append(stack, current.Content[index])
		}
	}
	return ctx.Err()
}

func writeFramedFingerprintBytesContext(ctx context.Context, destination hash.Hash, value []byte) error {
	var size [8]byte
	binary.BigEndian.PutUint64(size[:], uint64(len(value)))
	if err := writeHashBytesContext(ctx, destination, size[:]); err != nil {
		return err
	}
	return writeHashBytesContext(ctx, destination, value)
}

func writeFramedFingerprintStringContext(ctx context.Context, destination hash.Hash, value string) error {
	var size [8]byte
	binary.BigEndian.PutUint64(size[:], uint64(len(value)))
	if err := writeHashBytesContext(ctx, destination, size[:]); err != nil {
		return err
	}
	const chunkSize = 64 << 10
	for offset := 0; offset < len(value); offset += chunkSize {
		if err := ctx.Err(); err != nil {
			return err
		}
		end := min(offset+chunkSize, len(value))
		if _, err := io.WriteString(destination, value[offset:end]); err != nil {
			return err
		}
	}
	return ctx.Err()
}

func writeHashBytesContext(ctx context.Context, destination hash.Hash, value []byte) error {
	const chunkSize = 64 << 10
	for offset := 0; offset < len(value); offset += chunkSize {
		if err := ctx.Err(); err != nil {
			return err
		}
		end := min(offset+chunkSize, len(value))
		if _, err := destination.Write(value[offset:end]); err != nil {
			return err
		}
	}
	return ctx.Err()
}

func (p *presentation) validateAuditedOwnersContext(ctx context.Context, plan auditedBytePatches) error {
	if err := plan.validateSourceContext(ctx, p.data); err != nil {
		return err
	}
	ownerPatch, err := firstAuditedPatchOwnerContext(ctx, plan.patches)
	if err != nil {
		return err
	}
	if ownerPatch >= 0 {
		if err := validateMutationYAMLGraphContext(ctx, p.root); err != nil {
			if errors.Is(err, context.Canceled) || errors.Is(err, context.DeadlineExceeded) {
				return err
			}
			patch := plan.patches[ownerPatch]
			return yamlPresentationError("invalid_patch", ErrUnsupportedPresentation, invalidPatchSpan(len(p.data), patch.Start, patch.End))
		}
	}
	for index, patch := range plan.patches {
		if index&4095 == 0 {
			if err := ctx.Err(); err != nil {
				return err
			}
		}
		if patch.owner == nil {
			continue
		}
		owner, err := nodeAtPathContext(ctx, p.root, patch.owner.path)
		if err != nil {
			return err
		}
		if owner == nil || owner.Kind != patch.owner.kind || owner.Tag != patch.owner.tag ||
			owner.Value != patch.owner.value || owner.Anchor != patch.owner.anchor ||
			p.yamlNodeSpan(owner) != patch.owner.span || len(owner.Content) != len(patch.owner.contentKind) {
			return yamlPresentationError("invalid_patch", ErrUnsupportedPresentation, invalidPatchSpan(len(p.data), patch.Start, patch.End))
		}
		contentKindEqual, err := equalYAMLContentKindsContext(ctx, owner.Content, patch.owner.contentKind)
		if err != nil {
			return err
		}
		if !contentKindEqual {
			return yamlPresentationError("invalid_patch", ErrUnsupportedPresentation, invalidPatchSpan(len(p.data), patch.Start, patch.End))
		}
		fingerprint, err := p.yamlNodeFingerprintContext(ctx, owner)
		if err != nil {
			return err
		}
		if fingerprint != patch.owner.fingerprint {
			return yamlPresentationError("invalid_patch", ErrUnsupportedPresentation, invalidPatchSpan(len(p.data), patch.Start, patch.End))
		}
	}
	return ctx.Err()
}

func firstAuditedPatchOwnerContext(ctx context.Context, patches []auditedBytePatch) (int, error) {
	for index := range patches {
		if index&4095 == 0 {
			if err := ctx.Err(); err != nil {
				return -1, err
			}
		}
		if patches[index].owner != nil {
			return index, nil
		}
	}
	return -1, ctx.Err()
}

func equalYAMLContentKindsContext(ctx context.Context, content []*yaml.Node, kinds []yaml.Kind) (bool, error) {
	if len(content) != len(kinds) {
		return false, nil
	}
	for index, child := range content {
		if index&4095 == 0 {
			if err := ctx.Err(); err != nil {
				return false, err
			}
		}
		if child == nil || child.Kind != kinds[index] {
			return false, nil
		}
	}
	return true, ctx.Err()
}

func applyBytePatchesContext(ctx context.Context, in []byte, patches []bytePatch) ([]byte, error) {
	plan, err := auditBytePatchesContext(ctx, in, patches)
	if err != nil {
		return nil, err
	}
	return applyAuditedBytePatchesContext(ctx, in, plan)
}

func applyAuditedBytePatchesContext(ctx context.Context, in []byte, plan auditedBytePatches) ([]byte, error) {
	if err := plan.validateSourceContext(ctx, in); err != nil {
		return nil, err
	}
	if len(in) > plan.outputLen && len(plan.patches) == 0 {
		return nil, yamlPresentationError("invalid_patch", ErrUnsupportedPresentation, SourceSpan{})
	}
	out := make([]byte, plan.outputLen)
	sourceAt, outAt := len(in), len(out)
	for index := len(plan.patches) - 1; index >= 0; index-- {
		if err := ctx.Err(); err != nil {
			return nil, err
		}
		patch := plan.patches[index]
		trailing := in[patch.End:sourceAt]
		outAt -= len(trailing)
		if err := copyBytesIntoContext(ctx, out[outAt:outAt+len(trailing)], trailing); err != nil {
			return nil, err
		}
		outAt -= len(patch.Text)
		if err := copyBytesIntoContext(ctx, out[outAt:outAt+len(patch.Text)], patch.Text); err != nil {
			return nil, err
		}
		sourceAt = patch.Start
	}
	outAt -= sourceAt
	if outAt != 0 {
		return nil, yamlPresentationError("invalid_patch", ErrUnsupportedPresentation, SourceSpan{Start: 0, End: len(in)})
	}
	if err := copyBytesIntoContext(ctx, out[:sourceAt], in[:sourceAt]); err != nil {
		return nil, err
	}
	return out, ctx.Err()
}

func copyBytesIntoContext(ctx context.Context, destination, source []byte) error {
	if len(destination) != len(source) {
		return yamlPresentationError("invalid_patch", ErrUnsupportedPresentation, SourceSpan{Start: 0, End: len(source)})
	}
	const chunkSize = 64 << 10
	for len(source) > 0 {
		if err := ctx.Err(); err != nil {
			return err
		}
		n := min(len(source), chunkSize)
		copy(destination[:n], source[:n])
		destination, source = destination[n:], source[n:]
	}
	return ctx.Err()
}

func appendBytesContext(ctx context.Context, dst, source []byte) ([]byte, error) {
	const chunkSize = 64 << 10
	for len(source) > 0 {
		if err := ctx.Err(); err != nil {
			return nil, err
		}
		n := min(len(source), chunkSize)
		dst = append(dst, source[:n]...)
		source = source[n:]
	}
	if err := ctx.Err(); err != nil {
		return nil, err
	}
	return dst, nil
}

func (p *presentation) VerifyPatchedContext(ctx context.Context, out []byte, patches []bytePatch) error {
	if err := ctx.Err(); err != nil {
		return err
	}
	plan, err := p.auditBytePatchesContext(ctx, patches)
	if err != nil {
		return err
	}
	if err := p.validateAuditedOwnersContext(ctx, plan); err != nil {
		return err
	}
	if len(out) != plan.outputLen {
		return yamlPresentationError("invalid_patch", ErrUnsupportedPresentation, auditedBytePatchesDiagnosticSpan(len(p.data), plan.patches))
	}
	return p.verifyPatchedPlanContext(ctx, out, plan)
}

func (p *presentation) verifyPatchedPlanContext(ctx context.Context, out []byte, plan auditedBytePatches) error {
	patches := plan.patches
	after, err := parsePresentationContext(ctx, out)
	if err != nil {
		return err
	}
	hasContract := false
	for _, patch := range patches {
		if err := ctx.Err(); err != nil {
			return err
		}
		hasContract = hasContract || patch.edit != nil
	}
	if len(patches) == 0 {
		return nil
	}
	if !hasContract {
		return yamlPresentationError("missing_semantic_edit", ErrUnsupportedPresentation, auditedBytePatchesSpan(patches))
	}
	afterSemantic, err := semanticYAMLContext(ctx, after.root)
	if err != nil {
		return err
	}
	hasProjection := false
	for _, patch := range patches {
		if err := ctx.Err(); err != nil {
			return err
		}
		if patch.edit == nil {
			return yamlPresentationError("missing_semantic_edit", ErrUnsupportedPresentation, SourceSpan{Start: patch.Start, End: patch.End})
		}
		if patch.edit.expected != nil {
			hasProjection = true
			equal, err := equalSemanticYAMLContext(ctx, patch.edit.expected, afterSemantic)
			if err != nil {
				return err
			}
			if !equal {
				return yamlPresentationError("semantic_edit_mismatch", ErrUnsupportedPresentation, SourceSpan{Start: patch.Start, End: patch.End})
			}
		}
	}
	if hasProjection {
		return nil
	}
	expected, err := semanticYAMLContext(ctx, p.root)
	if err != nil {
		return err
	}
	for _, patch := range patches {
		if err := ctx.Err(); err != nil {
			return err
		}
		if patch.edit.expected != nil {
			continue
		}
		before, err := nodeAtPathContext(ctx, p.root, patch.edit.path)
		if err != nil {
			return err
		}
		afterNode, err := nodeAtPathContext(ctx, after.root, patch.edit.path)
		if err != nil {
			return err
		}
		if before == nil || afterNode == nil || before.Value != patch.edit.before || afterNode.Value != patch.edit.after {
			return yamlPresentationError("semantic_edit_mismatch", ErrUnsupportedPresentation, SourceSpan{Start: patch.Start, End: patch.End})
		}
		expectedNode := nodeAtPathNode(expected, patch.edit.path)
		if expectedNode == nil {
			return yamlPresentationError("semantic_edit_path", ErrUnsupportedPresentation, SourceSpan{Start: patch.Start, End: patch.End})
		}
		expectedNode.Value = patch.edit.after
	}
	equal, err := equalSemanticYAMLContext(ctx, expected, afterSemantic)
	if err != nil {
		return err
	}
	if !equal {
		return yamlPresentationError("semantic_projection_mismatch", ErrUnsupportedPresentation, auditedBytePatchesSpan(patches))
	}
	return nil
}

func bytePatchesSpan(patches []bytePatch) SourceSpan {
	if len(patches) == 0 {
		return SourceSpan{}
	}
	span := SourceSpan{Start: patches[0].Start, End: patches[0].End}
	for _, patch := range patches[1:] {
		if patch.Start < span.Start {
			span.Start = patch.Start
		}
		if patch.End > span.End {
			span.End = patch.End
		}
	}
	return span
}

func auditedBytePatchesSpan(patches []auditedBytePatch) SourceSpan {
	if len(patches) == 0 {
		return SourceSpan{}
	}
	span := SourceSpan{Start: patches[0].Start, End: patches[0].End}
	for _, patch := range patches[1:] {
		span.Start = min(span.Start, patch.Start)
		span.End = max(span.End, patch.End)
	}
	return span
}

func auditedBytePatchesDiagnosticSpan(sourceLength int, patches []auditedBytePatch) SourceSpan {
	span := auditedBytePatchesSpan(patches)
	if sourceLength <= 0 || span.Start != span.End {
		return span
	}
	if span.End < sourceLength {
		span.End++
	} else if span.Start > 0 {
		span.Start--
	}
	return span
}

func (p *presentation) patchYAMLContext(ctx context.Context, patches []bytePatch) ([]byte, error) {
	plan, err := p.auditBytePatchesContext(ctx, patches)
	if err != nil {
		return nil, err
	}
	if err := p.validateAuditedOwnersContext(ctx, plan); err != nil {
		return nil, err
	}
	out, err := applyAuditedBytePatchesContext(ctx, p.data, plan)
	if err != nil {
		return nil, err
	}
	if err := ctx.Err(); err != nil {
		return nil, err
	}
	if err := p.verifyPatchedPlanContext(ctx, out, plan); err != nil {
		// Verification reparses the generated buffer, whose offsets can exceed
		// the original source after an insertion. Public mutation diagnostics
		// always address the original file, so attribute a generated-output
		// failure to the original patch union and mark it full-file-relative.
		var presentation *PresentationError
		if errors.As(err, &presentation) {
			copy := *presentation
			copy.Location = auditedBytePatchesDiagnosticSpan(len(p.data), plan.patches)
			copy.yamlRelative = false
			return nil, &copy
		}
		return nil, err
	}
	equal, err := bytesOutsideAuditedPatchesEqualContext(ctx, p.data, out, plan)
	if err != nil {
		return nil, err
	}
	if !equal {
		return nil, yamlPresentationError("outside_span_changed", ErrUnsupportedPresentation, auditedBytePatchesDiagnosticSpan(len(p.data), plan.patches))
	}
	return out, nil
}

// bytesOutsidePatchesEqualContext compares unchanged source segments while
// allowing owned replacements to have different lengths.
func bytesOutsidePatchesEqualContext(ctx context.Context, before, after []byte, patches []bytePatch) (bool, error) {
	plan, err := auditBytePatchesContext(ctx, before, patches)
	if err != nil {
		return false, err
	}
	return bytesOutsideAuditedPatchesEqualContext(ctx, before, after, plan)
}

func bytesOutsideAuditedPatchesEqualContext(ctx context.Context, before, after []byte, plan auditedBytePatches) (bool, error) {
	if len(after) != plan.outputLen {
		return false, nil
	}
	oldAt, newAt := 0, 0
	for _, patch := range plan.patches {
		if err := ctx.Err(); err != nil {
			return false, err
		}
		width := patch.Start - oldAt
		if width < 0 || width > len(after)-newAt {
			return false, nil
		}
		equal, err := bytesEqualContext(ctx, before[oldAt:patch.Start], after[newAt:newAt+width])
		if err != nil {
			return false, err
		}
		if !equal {
			return false, nil
		}
		oldAt = patch.End
		newAt += width
		if len(patch.Text) > len(after)-newAt {
			return false, nil
		}
		newAt += len(patch.Text)
	}
	if newAt > len(after) {
		return false, nil
	}
	return bytesEqualContext(ctx, before[oldAt:], after[newAt:])
}

func bytesEqualContext(ctx context.Context, left, right []byte) (bool, error) {
	if len(left) != len(right) {
		return false, nil
	}
	const chunkSize = 64 << 10
	for len(left) > 0 {
		if err := ctx.Err(); err != nil {
			return false, err
		}
		n := min(len(left), chunkSize)
		if !bytes.Equal(left[:n], right[:n]) {
			return false, nil
		}
		left, right = left[n:], right[n:]
	}
	return len(left) == 0, ctx.Err()
}
