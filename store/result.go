package store

import (
	"bytes"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"sort"
	"strings"
	"time"
	"unicode/utf8"

	"github.com/skosovsky/okf/bundle"
)

// Read records one revision dependency of a preview. Path is a clean
// slash-separated source path. Reads contains every revision-visible base file
// (including files reached through a Snapshot or manifest fast path), plus
// meaningful read attempts outside that set. It is deterministic and unique;
// Preview is a dependency set, not an I/O trace.
type Read struct {
	Path string
}

// DiagnosticKind identifies the producer of a preview finding.
type DiagnosticKind string

const (
	DiagnosticValidation DiagnosticKind = "validation"
	DiagnosticRelation   DiagnosticKind = "relation"
)

// DiagnosticSeverity is the backend-neutral impact of a preview finding.
type DiagnosticSeverity string

const (
	DiagnosticError   DiagnosticSeverity = "error"
	DiagnosticWarning DiagnosticSeverity = "warning"
	DiagnosticInfo    DiagnosticSeverity = "info"
)

// Write records one staged revision-visible file. Digest is the lowercase
// SHA-256 digest of Content. Implementations return Content as a copy.
type Write struct {
	Path, Digest string
	Content      []byte
}

// Rename records a staged move between slash-separated revision-visible paths.
type Rename struct {
	From, To string
}

// FileChangeKind classifies one planned filesystem-visible change.
type FileChangeKind string

const (
	FileWrite  FileChangeKind = "write"
	FileDelete FileChangeKind = "delete"
	FileRename FileChangeKind = "rename"
)

// FileChange describes a deterministic planned change. For FileRename, From
// and Path are populated; for writes and deletes, Path is populated.
type FileChange struct {
	Kind       FileChangeKind
	Path, From string
}

// OperationPlan is the normalized result of one semantic operation. Details
// are stable, backend-neutral explanatory strings and never authorization data.
type OperationPlan struct {
	Operation    Operation
	AffectedRefs []bundle.RelationRef
	Details      []string
}

// Preview is an immutable staged result. All slices are ordered deterministically
// by implementations and are caller-owned copies. An invalid preview must not
// expose a commit-ready plan and is returned as an error instead.
type Preview struct {
	BaseRevision, ResultRevision Revision
	Reads                        []Read
	Writes                       []Write
	Deletes                      []string
	Renames                      []Rename
	AffectedRefs, ReverseImpact  []bundle.RelationRef
	Plan                         []OperationPlan
	Diagnostics                  []Diagnostic
}

// Clone returns a deep copy of Preview's slice fields (operations are immutable values).
func (p Preview) Clone() Preview {
	p.Reads = cloneSlice(p.Reads)
	p.Writes = cloneWrites(p.Writes)
	p.Deletes = cloneSlice(p.Deletes)
	p.Renames = cloneSlice(p.Renames)
	p.AffectedRefs = cloneRefs(p.AffectedRefs)
	p.ReverseImpact = cloneRefs(p.ReverseImpact)
	p.Diagnostics = cloneDiagnostics(p.Diagnostics)
	p.Plan = clonePlan(p.Plan)
	return p
}

// CommitReceiptFormatVersion identifies the persisted receipt format.
const CommitReceiptFormatVersion uint16 = 1

// CommitReceipt records one durable successful commit or idempotent replay.
// CommitTime is UTC and RequestDigest is the canonical ChangeSet digest.
type CommitReceipt struct {
	FormatVersion                uint16
	ChangeSetID                  ChangeSetID
	IdempotencyKey               IdempotencyKey
	RequestDigest                string
	BaseRevision, ResultRevision Revision
	CommitTime                   time.Time
	ChangedRefs                  []bundle.RelationRef
	ChangedFiles                 []FileChange
}

// MarshalJSON persists relation refs in their canonical string form. ConceptID
// intentionally keeps its segments private, so default struct encoding would
// silently turn every persisted ref into an empty object.
func (r CommitReceipt) MarshalJSON() ([]byte, error) {
	if err := ValidateCommitReceipt(r); err != nil {
		return nil, err
	}
	type wire struct {
		FormatVersion  uint16         `json:"FormatVersion"`
		ChangeSetID    ChangeSetID    `json:"ChangeSetID"`
		IdempotencyKey IdempotencyKey `json:"IdempotencyKey"`
		RequestDigest  string         `json:"RequestDigest"`
		BaseRevision   Revision       `json:"BaseRevision"`
		ResultRevision Revision       `json:"ResultRevision"`
		CommitTime     time.Time      `json:"CommitTime"`
		ChangedRefs    []string       `json:"ChangedRefs"`
		ChangedFiles   []FileChange   `json:"ChangedFiles"`
	}
	orderedRefs := cloneRefs(r.ChangedRefs)
	sort.Slice(orderedRefs, func(i, j int) bool {
		return orderedRefs[i].String() < orderedRefs[j].String()
	})
	refs := make([]string, len(orderedRefs))
	for i, ref := range orderedRefs {
		refs[i] = ref.String()
	}
	// Durable receipts use non-null arrays even for an empty change set; nil
	// would be a second, ambiguous wire representation of the same receipt.
	files := make([]FileChange, len(r.ChangedFiles))
	copy(files, r.ChangedFiles)
	sort.Slice(files, func(i, j int) bool {
		return fileChangeIdentityOf(files[i]).less(fileChangeIdentityOf(files[j]))
	})
	return json.Marshal(wire{FormatVersion: r.FormatVersion, ChangeSetID: r.ChangeSetID, IdempotencyKey: r.IdempotencyKey, RequestDigest: r.RequestDigest, BaseRevision: r.BaseRevision, ResultRevision: r.ResultRevision, CommitTime: r.CommitTime.UTC(), ChangedRefs: refs, ChangedFiles: files})
}

// UnmarshalJSON accepts only the canonical receipt serialization. Receipts are
// durable commit evidence, so accepting ambiguous JSON here would make public
// transport and on-disk recovery disagree about the same value.
func (r *CommitReceipt) UnmarshalJSON(data []byte) error {
	decoded, err := decodeCommitReceipt(data)
	if err != nil {
		return err
	}
	*r = decoded
	return nil
}

func decodeCommitReceipt(data []byte) (CommitReceipt, error) {
	// encoding/json replaces malformed UTF-8 in strings. A receipt is durable
	// commit evidence, so reject malformed bytes before the duplicate-key scan
	// or any decoding can normalize its public or nested string fields.
	if !utf8.Valid(data) {
		return CommitReceipt{}, invalidReceipt(errors.New("receipt contains invalid UTF-8"))
	}
	if err := RejectDuplicateJSONKeys(data); err != nil {
		return CommitReceipt{}, invalidReceipt(err)
	}
	type wire struct {
		FormatVersion  uint16         `json:"FormatVersion"`
		ChangeSetID    ChangeSetID    `json:"ChangeSetID"`
		IdempotencyKey IdempotencyKey `json:"IdempotencyKey"`
		RequestDigest  string         `json:"RequestDigest"`
		BaseRevision   Revision       `json:"BaseRevision"`
		ResultRevision Revision       `json:"ResultRevision"`
		CommitTime     time.Time      `json:"CommitTime"`
		ChangedRefs    []string       `json:"ChangedRefs"`
		ChangedFiles   []FileChange   `json:"ChangedFiles"`
	}
	var fields map[string]json.RawMessage
	if err := json.Unmarshal(data, &fields); err != nil {
		return CommitReceipt{}, invalidReceipt(err)
	}
	required := []string{"FormatVersion", "ChangeSetID", "IdempotencyKey", "RequestDigest", "BaseRevision", "ResultRevision", "CommitTime", "ChangedRefs", "ChangedFiles"}
	if len(fields) != len(required) {
		return CommitReceipt{}, invalidReceipt(errors.New("receipt fields are incomplete or unknown"))
	}
	for _, name := range required {
		if value, ok := fields[name]; !ok || bytes.Equal(value, []byte("null")) {
			return CommitReceipt{}, invalidReceipt(fmt.Errorf("missing receipt field %q", name))
		}
	}
	if err := validateCommitReceiptWire(fields); err != nil {
		return CommitReceipt{}, invalidReceipt(err)
	}
	var w wire
	decoder := json.NewDecoder(bytes.NewReader(data))
	decoder.DisallowUnknownFields()
	if err := decoder.Decode(&w); err != nil {
		return CommitReceipt{}, invalidReceipt(err)
	}
	if err := requireJSONEOF(decoder); err != nil {
		return CommitReceipt{}, invalidReceipt(err)
	}
	refs := make([]bundle.RelationRef, len(w.ChangedRefs))
	for i, raw := range w.ChangedRefs {
		ref, err := parseCanonicalReceiptRef(raw)
		if err != nil {
			return CommitReceipt{}, invalidReceipt(fmt.Errorf("invalid changed ref %q: %w", raw, err))
		}
		refs[i] = ref
	}
	r := CommitReceipt{FormatVersion: w.FormatVersion, ChangeSetID: w.ChangeSetID, IdempotencyKey: w.IdempotencyKey, RequestDigest: w.RequestDigest, BaseRevision: w.BaseRevision, ResultRevision: w.ResultRevision, CommitTime: w.CommitTime, ChangedRefs: refs, ChangedFiles: w.ChangedFiles}
	if err := ValidateCommitReceipt(r); err != nil {
		return CommitReceipt{}, err
	}
	canonical, err := json.Marshal(r)
	if err != nil || !bytes.Equal(data, canonical) {
		return CommitReceipt{}, invalidReceipt(errors.New("receipt is not canonical JSON"))
	}
	return r, nil
}

// validateCommitReceiptWire closes the typed-zero gap before encoding/json
// turns null slices into nil and omitted FileChange.From into an empty string.
// It intentionally validates the raw transport shape; semantic/canonical value
// validation remains centralized in ValidateCommitReceipt below.
func validateCommitReceiptWire(fields map[string]json.RawMessage) error {
	if !bytes.Equal(fields["FormatVersion"], []byte("1")) {
		return errors.New("receipt FormatVersion must be canonical integer 1")
	}
	for _, name := range []string{"ChangeSetID", "IdempotencyKey", "RequestDigest", "BaseRevision", "ResultRevision", "CommitTime"} {
		if !receiptJSONString(fields[name]) {
			return fmt.Errorf("receipt %s must be a non-null string", name)
		}
	}
	for _, name := range []string{"ChangedRefs", "ChangedFiles"} {
		if !receiptJSONArray(fields[name]) {
			return fmt.Errorf("receipt %s must be a non-null array", name)
		}
	}
	var refs []json.RawMessage
	if err := json.Unmarshal(fields["ChangedRefs"], &refs); err != nil {
		return err
	}
	for _, ref := range refs {
		if !receiptJSONString(ref) {
			return errors.New("receipt changed ref must be a string")
		}
	}
	var files []json.RawMessage
	if err := json.Unmarshal(fields["ChangedFiles"], &files); err != nil {
		return err
	}
	for _, raw := range files {
		var file map[string]json.RawMessage
		if !receiptJSONObject(raw) || json.Unmarshal(raw, &file) != nil || len(file) != 3 {
			return errors.New("receipt changed file has an invalid field set")
		}
		for _, name := range []string{"Kind", "Path", "From"} {
			if value, ok := file[name]; !ok || !receiptJSONString(value) {
				return fmt.Errorf("receipt changed file %s must be a non-null string", name)
			}
		}
	}
	return nil
}

func receiptJSONString(raw json.RawMessage) bool {
	if len(raw) < 2 || raw[0] != '"' || raw[len(raw)-1] != '"' {
		return false
	}
	var value string
	return json.Unmarshal(raw, &value) == nil && utf8.ValidString(value)
}

func receiptJSONArray(raw json.RawMessage) bool {
	var values []json.RawMessage
	return len(raw) >= 2 && raw[0] == '[' && raw[len(raw)-1] == ']' && json.Unmarshal(raw, &values) == nil
}

func receiptJSONObject(raw json.RawMessage) bool {
	var value map[string]json.RawMessage
	return len(raw) >= 2 && raw[0] == '{' && raw[len(raw)-1] == '}' && json.Unmarshal(raw, &value) == nil && value != nil
}

// ValidateCommitReceipt validates the durable/public receipt contract. A
// receipt without an idempotency key is legitimate, but it still carries its
// request digest because journals use that digest as their transaction ID.
func ValidateCommitReceipt(r CommitReceipt) error {
	if r.FormatVersion != CommitReceiptFormatVersion {
		return invalidReceipt(errors.New("unsupported receipt format version"))
	}
	if err := r.ChangeSetID.Validate(); err != nil {
		return invalidReceipt(fmt.Errorf("invalid receipt change set id: %w", err))
	}
	if r.IdempotencyKey != "" {
		if err := r.IdempotencyKey.Validate(); err != nil {
			return invalidReceipt(fmt.Errorf("invalid receipt idempotency key: %w", err))
		}
	}
	if !validReceiptDigest(r.RequestDigest) {
		return invalidReceipt(errors.New("invalid receipt request digest"))
	}
	if !r.BaseRevision.Valid() || !r.ResultRevision.Valid() {
		return invalidReceipt(errors.New("invalid receipt revisions"))
	}
	name, offset := r.CommitTime.Zone()
	if r.CommitTime.IsZero() || name != "UTC" || offset != 0 {
		return invalidReceipt(errors.New("receipt commit time must be non-zero UTC"))
	}
	seenRefs := make(map[relationRefIdentity]struct{}, len(r.ChangedRefs))
	previous := ""
	havePrevious := false
	for _, ref := range r.ChangedRefs {
		value := ref.String()
		parsed, err := parseCanonicalReceiptRef(value)
		identity := relationRefIdentityOf(ref)
		if err != nil || relationRefIdentityOf(parsed) != identity {
			return invalidReceipt(errors.New("receipt changed refs must be valid, sorted, and unique"))
		}
		if _, duplicate := seenRefs[identity]; duplicate || havePrevious && value <= previous {
			return invalidReceipt(errors.New("receipt changed refs must be valid, sorted, and unique"))
		}
		seenRefs[identity] = struct{}{}
		previous, havePrevious = value, true
	}
	var previousFile fileChangeIdentity
	havePreviousFile := false
	for _, file := range r.ChangedFiles {
		if !validReceiptFileChange(file) {
			return invalidReceipt(errors.New("invalid receipt changed file"))
		}
		identity := fileChangeIdentityOf(file)
		if havePreviousFile && !previousFile.less(identity) {
			return invalidReceipt(errors.New("receipt changed files must be sorted and unique"))
		}
		previousFile, havePreviousFile = identity, true
	}
	return nil
}

func parseCanonicalReceiptRef(raw string) (bundle.RelationRef, error) {
	ref, err := bundle.ParseRelationRef(raw)
	if err != nil {
		return bundle.RelationRef{}, err
	}
	if ref.String() != raw {
		return bundle.RelationRef{}, errors.New("relation ref is not canonically serialized")
	}
	return ref, nil
}

type fileChangeIdentity struct {
	path string
	kind FileChangeKind
	from string
}

func fileChangeIdentityOf(file FileChange) fileChangeIdentity {
	return fileChangeIdentity{path: file.Path, kind: file.Kind, from: file.From}
}

func (left fileChangeIdentity) less(right fileChangeIdentity) bool {
	if left.path != right.path {
		return left.path < right.path
	}
	if left.kind != right.kind {
		return left.kind < right.kind
	}
	return left.from < right.from
}

func validReceiptDigest(value string) bool {
	const prefix = "sha256:"
	if !strings.HasPrefix(value, prefix) || len(value) != len(prefix)+sha256.Size*2 || value != strings.ToLower(value) {
		return false
	}
	_, err := hex.DecodeString(value[len(prefix):])
	return err == nil
}

func validReceiptFileChange(file FileChange) bool {
	if bundle.ValidateRevisionPath(file.Path) != nil {
		return false
	}
	switch file.Kind {
	case FileWrite, FileDelete:
		return file.From == ""
	case FileRename:
		return bundle.ValidateRevisionPath(file.From) == nil && file.From != file.Path
	default:
		return false
	}
}

func invalidReceipt(err error) error {
	return fmt.Errorf("receipt serialization: %w", errors.Join(ErrStorageCorrupt, err))
}

func requireJSONEOF(decoder *json.Decoder) error {
	if err := decoder.Decode(&struct{}{}); err != io.EOF {
		if err == nil {
			return errors.New("trailing JSON data")
		}
		return err
	}
	return nil
}

// RejectDuplicateJSONKeys rejects duplicate keys at every JSON object depth and
// trailing values. Durable filesystem envelopes use it too, so public receipt
// decoding and crash recovery share the same ambiguity boundary.
func RejectDuplicateJSONKeys(data []byte) error {
	decoder := json.NewDecoder(bytes.NewReader(data))
	if err := walkJSONValue(decoder); err != nil {
		return err
	}
	return requireJSONEOF(decoder)
}

func walkJSONValue(decoder *json.Decoder) error {
	token, err := decoder.Token()
	if err != nil {
		return err
	}
	delim, ok := token.(json.Delim)
	if !ok {
		return nil
	}
	switch delim {
	case '{':
		seen := map[string]struct{}{}
		for decoder.More() {
			name, err := decoder.Token()
			if err != nil {
				return err
			}
			key, ok := name.(string)
			if !ok {
				return errors.New("JSON object key is not a string")
			}
			if _, exists := seen[key]; exists {
				return fmt.Errorf("duplicate JSON key %q", key)
			}
			seen[key] = struct{}{}
			if err := walkJSONValue(decoder); err != nil {
				return err
			}
		}
		_, err = decoder.Token()
		return err
	case '[':
		for decoder.More() {
			if err := walkJSONValue(decoder); err != nil {
				return err
			}
		}
		_, err = decoder.Token()
		return err
	default:
		return errors.New("invalid JSON delimiter")
	}
}

// Clone returns a copy safe for caller mutation.
func (r CommitReceipt) Clone() CommitReceipt {
	r.ChangedRefs = cloneRefs(r.ChangedRefs)
	r.ChangedFiles = cloneSlice(r.ChangedFiles)
	return r
}

func cloneRefs(in []bundle.RelationRef) []bundle.RelationRef {
	return cloneSlice(in)
}
func cloneWrites(in []Write) []Write {
	out := cloneSlice(in)
	for i := range out {
		out[i].Content = cloneSlice(out[i].Content)
	}
	return out
}
func cloneDiagnostics(in []Diagnostic) []Diagnostic {
	out := cloneSlice(in)
	for i := range out {
		out[i].Refs = cloneRefs(out[i].Refs)
	}
	return out
}
func clonePlan(in []OperationPlan) []OperationPlan {
	out := cloneSlice(in)
	for i := range out {
		out[i].AffectedRefs = cloneRefs(out[i].AffectedRefs)
		out[i].Details = cloneSlice(out[i].Details)
	}
	return out
}

// cloneSlice preserves the caller-visible distinction between nil and an
// allocated empty slice while returning independent backing storage.
func cloneSlice[S ~[]E, E any](in S) S {
	if in == nil {
		return nil
	}
	out := make(S, len(in))
	copy(out, in)
	return out
}
