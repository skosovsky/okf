package bundle

import (
	"bytes"
	"context"
	"encoding/hex"
	"encoding/json"
	"errors"
	"io/fs"
	"os"
	"sort"
)

type detachedDocumentArtifact struct {
	discovery publicationNamespaceDiscovery
	kind      string
	spec      publicationFileSpec
	err       error
	attached  bool
}

type documentV2TerminalCleanupForm uint8

const (
	documentV2TerminalInvalid documentV2TerminalCleanupForm = iota
	documentV2TerminalCommit
	documentV2TerminalUntouched
	documentV2TerminalPreNewRestored
	documentV2TerminalPublishedRestored
	documentV2TerminalRestoredPrefix
)

type documentV2TerminalCleanupPlan struct {
	form      documentV2TerminalCleanupForm
	proofKind string
	order     []string
}

func isDetachedDocumentArtifact(kind string) bool {
	if kind == "witness" ||
		kind == "backup" ||
		kind == "stage" ||
		kind == "new-install" ||
		kind == "restore-install" ||
		kind == "discard" {
		return true
	}
	for slot := 0; slot < maxDocumentRollbackAnchors; slot++ {
		if kind == documentRollbackAnchorProtocolKind(slot) {
			return true
		}
	}
	return false
}

func documentDetachedArtifactName(
	kind string,
	target string,
	spec publicationFileSpec,
) (string, bool) {
	switch kind {
	case "witness", "backup", "stage", "new-install", "restore-install", "discard":
		return documentBoundArtifactName(kind, target, spec.mode, spec.data), true
	default:
		for slot := 0; slot < maxDocumentRollbackAnchors; slot++ {
			if kind == documentRollbackAnchorProtocolKind(slot) {
				return documentRollbackAnchorName(target, spec.mode, spec.data, slot), true
			}
		}
	}
	return "", false
}

func attachDocumentDetachedArtifacts(
	group *documentRecoveryGroup,
	detached []*detachedDocumentArtifact,
) error {
	attach := func(artifact *detachedDocumentArtifact) error {
		if _, duplicate := group.names[artifact.kind]; duplicate {
			return publicationConflict("duplicate document detached artifact kind", nil)
		}
		group.names[artifact.kind] = artifact.discovery.name
		group.protocolNames[artifact.kind] = artifact.discovery.protocolName
		if artifact.err != nil {
			group.invalid[artifact.kind] = publicationConflict(
				"document "+artifact.kind+" artifact is not an inspectable regular file",
				artifact.err,
			)
			group.foreign[artifact.kind] = artifact.discovery
		} else {
			group.artifacts[artifact.kind] = artifact.spec
		}
		artifact.attached = true
		return nil
	}
	for _, artifact := range detached {
		if artifact.attached || artifact.discovery.directory != group.directory {
			continue
		}
		if artifact.err != nil {
			var expected string
			switch artifact.kind {
			case "witness", "backup":
				expected = documentBoundArtifactNameForContract(
					artifact.kind,
					group.manifest.Target,
					fs.FileMode(group.manifest.Mode),
					group.manifest.OriginalSize,
					group.manifest.OriginalSHA256,
				)
			}
			if expected == "" || expected != artifact.discovery.protocolName {
				continue
			}
			if err := attach(artifact); err != nil {
				return err
			}
			continue
		}
		expected, ok := documentDetachedArtifactName(
			artifact.kind,
			group.manifest.Target,
			artifact.spec,
		)
		if !ok || expected != artifact.discovery.protocolName {
			continue
		}
		if err := attach(artifact); err != nil {
			return err
		}
	}
	var originalData []byte
	for _, kind := range []string{
		"backup",
		"witness",
		"claim",
		documentRollbackAnchorProtocolKind(0),
	} {
		spec, present := group.artifacts[kind]
		if !present ||
			spec.mode != fs.FileMode(group.manifest.Mode) ||
			uint64(spec.size) != group.manifest.OriginalSize ||
			hex.EncodeToString(spec.digest[:]) != group.manifest.OriginalSHA256 {
			continue
		}
		originalData = spec.data
		break
	}
	if originalData == nil {
		return nil
	}
	original := publicationFileSpec{
		mode: fs.FileMode(group.manifest.Mode),
		data: originalData,
	}
	for _, artifact := range detached {
		if artifact.attached || artifact.discovery.directory != group.directory {
			continue
		}
		switch {
		case artifact.kind == "witness", artifact.kind == "backup":
		default:
			isAnchor := false
			for slot := 0; slot < maxDocumentRollbackAnchors; slot++ {
				if artifact.kind == documentRollbackAnchorProtocolKind(slot) {
					isAnchor = true
					break
				}
			}
			if !isAnchor {
				continue
			}
		}
		expected, ok := documentDetachedArtifactName(
			artifact.kind,
			group.manifest.Target,
			original,
		)
		if !ok || expected != artifact.discovery.protocolName {
			continue
		}
		if err := attach(artifact); err != nil {
			return err
		}
	}
	return nil
}

func prepareTerminalDocumentRecoveryGroups(
	ctx context.Context,
	root *os.Root,
	detached []*detachedDocumentArtifact,
) ([]*documentRecoveryGroup, error) {
	byTarget := make(map[string]*documentRecoveryGroup)
	var groups []*documentRecoveryGroup
	fail := func(err error) ([]*documentRecoveryGroup, error) {
		for _, group := range groups {
			_ = group.parent.Close()
		}
		return nil, err
	}
	for _, artifact := range detached {
		if artifact.attached {
			continue
		}
		if artifact.kind == "claim" {
			continue
		}
		parent, err := openDirectory(root, artifact.discovery.directory)
		if err != nil {
			return fail(err)
		}
		target, leafSpec, err := inferDetachedDocumentTarget(
			ctx,
			parent,
			artifact.discovery.directory,
			artifact.kind,
			artifact.discovery.protocolName,
			artifact.spec,
		)
		if err != nil {
			_ = parent.Close()
			return fail(err)
		}
		group := byTarget[target]
		if group == nil {
			group = &documentRecoveryGroup{
				root: root, directory: artifact.discovery.directory, parent: parent,
				parentInfo: artifact.discovery.parentInfo,
				id:         artifact.discovery.protocolName,
				manifest: documentTransactionManifest{
					Format:         documentTransactionFormat,
					Target:         target,
					Mode:           uint32(artifact.spec.mode),
					OriginalSize:   uint64(artifact.spec.size),
					OriginalSHA256: hex.EncodeToString(artifact.spec.digest[:]),
				},
				artifacts:     make(map[string]publicationFileSpec),
				names:         make(map[string]string),
				protocolNames: make(map[string]string),
				invalid:       make(map[string]error),
				foreign:       make(map[string]publicationNamespaceDiscovery),
				leafState:     documentLeafOriginal,
				leafSpec:      leafSpec,
				terminalOnly:  true,
			}
			byTarget[target] = group
			groups = append(groups, group)
		} else {
			_ = parent.Close()
			if group.parentInfo == nil ||
				artifact.discovery.parentInfo == nil ||
				!os.SameFile(group.parentInfo, artifact.discovery.parentInfo) {
				return fail(publicationConflict("terminal document proof parent changed", nil))
			}
		}
		if _, duplicate := group.names[artifact.kind]; duplicate {
			return fail(publicationConflict("duplicate terminal document proof", nil))
		}
		group.artifacts[artifact.kind] = artifact.spec
		group.names[artifact.kind] = artifact.discovery.name
		group.protocolNames[artifact.kind] = artifact.discovery.protocolName
	}
	if err := attachTerminalDocumentClaims(groups, detached); err != nil {
		return fail(err)
	}
	for _, group := range groups {
		if err := validateTerminalDocumentRecoveryGroup(group); err != nil {
			return fail(err)
		}
	}
	return groups, nil
}

func attachTerminalDocumentClaims(
	groups []*documentRecoveryGroup,
	detached []*detachedDocumentArtifact,
) error {
	for _, artifact := range detached {
		if artifact.attached || artifact.kind != "claim" {
			continue
		}
		var owner *documentRecoveryGroup
		for _, group := range groups {
			if group.directory != artifact.discovery.directory {
				continue
			}
			witness, present := group.artifacts["witness"]
			if !present ||
				witness.info == nil ||
				artifact.spec.info == nil ||
				!os.SameFile(witness.info, artifact.spec.info) ||
				!sameDocumentArtifactPayload(witness, artifact.spec) {
				continue
			}
			manifest, ok := reconstructTerminalDocumentManifest(group)
			if !ok {
				continue
			}
			canonical, err := json.Marshal(manifest)
			if err != nil {
				return err
			}
			expected := documentArtifactName("claim", documentManifestDigest(canonical))
			if artifact.discovery.protocolName != expected {
				continue
			}
			if owner != nil {
				return publicationConflict("terminal document claim ownership is ambiguous", nil)
			}
			owner = group
		}
		if owner == nil {
			return publicationConflict("terminal document claim has no exact v2 owner", nil)
		}
		if _, duplicate := owner.names["claim"]; duplicate {
			return publicationConflict("duplicate terminal document claim", nil)
		}
		owner.artifacts["claim"] = artifact.spec
		owner.names["claim"] = artifact.discovery.name
		owner.protocolNames["claim"] = artifact.discovery.protocolName
		artifact.attached = true
	}
	return nil
}

func reconstructTerminalDocumentManifest(
	group *documentRecoveryGroup,
) (documentTransactionManifest, bool) {
	witness, hasWitness := group.artifacts["witness"]
	stage, hasStage := group.artifacts["stage"]
	if !hasWitness ||
		!hasStage ||
		witness.info == nil ||
		stage.info == nil ||
		witness.mode != stage.mode ||
		sameDocumentArtifactPayload(witness, stage) {
		return documentTransactionManifest{}, false
	}
	manifest := documentTransactionManifest{
		Format:          documentTransactionFormat,
		Target:          group.manifest.Target,
		Mode:            uint32(witness.mode),
		OriginalSize:    uint64(witness.size),
		OriginalSHA256:  hex.EncodeToString(witness.digest[:]),
		PublishedSize:   uint64(stage.size),
		PublishedSHA256: hex.EncodeToString(stage.digest[:]),
	}
	manifest.Stage = pathJoin(
		group.directory,
		documentBoundArtifactName("stage", manifest.Target, stage.mode, stage.data),
	)
	manifest.NewInstall = pathJoin(
		group.directory,
		documentBoundArtifactName("new-install", manifest.Target, stage.mode, stage.data),
	)
	manifest.Anchor = pathJoin(
		group.directory,
		documentRollbackAnchorName(manifest.Target, witness.mode, witness.data, 0),
	)
	manifest.RestoreInstall = pathJoin(
		group.directory,
		documentBoundArtifactName("restore-install", manifest.Target, witness.mode, witness.data),
	)
	manifest.Discard = pathJoin(
		group.directory,
		documentBoundArtifactName("discard", manifest.Target, stage.mode, stage.data),
	)
	return manifest, true
}

func inferDetachedDocumentTarget(
	ctx context.Context,
	parent *os.Root,
	directory string,
	kind string,
	protocolName string,
	proof publicationFileSpec,
) (string, publicationFileSpec, error) {
	file, err := parent.Open(".")
	if err != nil {
		return "", publicationFileSpec{}, err
	}
	entries, readErr := file.ReadDir(-1)
	closeErr := file.Close()
	if err := errors.Join(readErr, closeErr); err != nil {
		return "", publicationFileSpec{}, err
	}
	sort.Slice(entries, func(left, right int) bool {
		return entries[left].Name() < entries[right].Name()
	})
	var target string
	var leafSpec publicationFileSpec
	matches := 0
	for _, entry := range entries {
		if err := ctx.Err(); err != nil {
			return "", publicationFileSpec{}, err
		}
		name := entry.Name()
		if isReservedTransactionPath(name) {
			continue
		}
		candidate := pathJoin(directory, name)
		expected, ok := documentDetachedArtifactName(kind, candidate, proof)
		if !ok || expected != protocolName {
			continue
		}
		matches++
		target = candidate
		captured, captureErr := capturePublicationFile(ctx, parent, name)
		if captureErr != nil {
			return "", publicationFileSpec{}, publicationConflict(
				"self-bound document proof target is not a regular file",
				captureErr,
			)
		}
		leafSpec = captured
	}
	if matches != 1 {
		return "", publicationFileSpec{}, publicationConflict(
			"self-bound document proof target is missing or ambiguous",
			nil,
		)
	}
	return target, leafSpec, nil
}

func validateTerminalDocumentRecoveryGroup(group *documentRecoveryGroup) error {
	if matched, err := planDocumentV2TerminalCommit(group); matched || err != nil {
		return err
	}
	if matched, err := planDocumentV2TerminalUntouched(group); matched || err != nil {
		return err
	}
	if matched, err := planDocumentV2TerminalRestored(group); matched || err != nil {
		return err
	}
	return publicationConflict("document no-manifest evidence is not a terminal v2 cleanup prefix", nil)
}

func planDocumentV2TerminalCommit(
	group *documentRecoveryGroup,
) (bool, error) {
	stage, hasStage := group.artifacts["stage"]
	if !hasStage ||
		stage.info == nil ||
		group.leafSpec.info == nil ||
		!os.SameFile(stage.info, group.leafSpec.info) ||
		!sameDocumentArtifactPayload(stage, group.leafSpec) {
		return false, nil
	}
	for kind := range group.artifacts {
		switch kind {
		case "witness", "backup", "stage", "claim":
		default:
			return true, publicationConflict(
				"terminal committed document has forbidden evidence",
				nil,
			)
		}
	}
	witness, hasWitness := group.artifacts["witness"]
	backup, hasBackup := group.artifacts["backup"]
	claim, hasClaim := group.artifacts["claim"]
	validPrefix := !hasWitness && !hasBackup && !hasClaim ||
		hasWitness && !hasBackup && !hasClaim ||
		hasWitness && hasBackup && !hasClaim ||
		hasWitness && hasBackup && hasClaim
	if !validPrefix {
		return true, publicationConflict(
			"terminal committed document is not a reachable cleanup prefix",
			nil,
		)
	}
	if hasWitness {
		if witness.info == nil ||
			witness.mode != stage.mode ||
			sameDocumentArtifactPayload(witness, stage) ||
			os.SameFile(witness.info, stage.info) {
			return true, publicationConflict(
				"terminal committed witness is not an independent original",
				nil,
			)
		}
	}
	if hasBackup {
		if backup.info == nil ||
			!sameDocumentArtifactPayload(backup, witness) ||
			os.SameFile(backup.info, witness.info) ||
			os.SameFile(backup.info, stage.info) {
			return true, publicationConflict(
				"terminal committed backup is not an independent original copy",
				nil,
			)
		}
	}
	if hasClaim {
		if claim.info == nil ||
			!sameDocumentArtifactPayload(claim, witness) ||
			!os.SameFile(claim.info, witness.info) {
			return true, publicationConflict(
				"terminal committed claim is detached from witness",
				nil,
			)
		}
		manifest, ok := reconstructTerminalDocumentManifest(group)
		if !ok {
			return true, publicationConflict(
				"terminal committed claim lacks reconstructable manifest",
				nil,
			)
		}
		canonical, err := json.Marshal(manifest)
		if err != nil {
			return true, err
		}
		expected := documentArtifactName("claim", documentManifestDigest(canonical))
		if group.protocolNames["claim"] != expected {
			return true, publicationConflict(
				"terminal committed claim is not bound to reconstructed manifest",
				nil,
			)
		}
		group.manifest = manifest
	} else if hasWitness {
		if manifest, ok := reconstructTerminalDocumentManifest(group); ok {
			group.manifest = manifest
		}
	}
	group.proofKind = "stage"
	group.leafState = documentLeafPublished
	group.manifest.Mode = uint32(stage.mode)
	group.manifest.PublishedSize = uint64(stage.size)
	group.manifest.PublishedSHA256 = hex.EncodeToString(stage.digest[:])
	group.terminalPlan = documentV2TerminalCleanupPlan{
		form:      documentV2TerminalCommit,
		proofKind: "stage",
		order:     []string{"claim", "backup", "witness", "stage"},
	}
	return true, nil
}

func planDocumentV2TerminalUntouched(
	group *documentRecoveryGroup,
) (bool, error) {
	witness, hasWitness := group.artifacts["witness"]
	if !hasWitness ||
		witness.info == nil ||
		group.leafSpec.info == nil ||
		!os.SameFile(witness.info, group.leafSpec.info) ||
		!sameDocumentArtifactPayload(witness, group.leafSpec) {
		return false, nil
	}
	for kind := range group.artifacts {
		switch kind {
		case "witness", "backup", "stage", "new-install":
		default:
			return true, publicationConflict(
				"terminal untouched document has forbidden evidence",
				nil,
			)
		}
	}
	backup, hasBackup := group.artifacts["backup"]
	stage, hasStage := group.artifacts["stage"]
	newInstall, hasNew := group.artifacts["new-install"]
	validPrefix := !hasBackup && !hasStage && !hasNew ||
		hasBackup && !hasStage && !hasNew ||
		hasBackup && hasStage && !hasNew ||
		hasBackup && hasStage && hasNew
	if !validPrefix {
		return true, publicationConflict(
			"terminal untouched document is not a reachable cleanup prefix",
			nil,
		)
	}
	if hasBackup {
		if backup.info == nil ||
			!sameDocumentArtifactPayload(backup, witness) ||
			os.SameFile(backup.info, witness.info) {
			return true, publicationConflict(
				"terminal untouched backup is not an independent original copy",
				nil,
			)
		}
	}
	if hasStage {
		if stage.info == nil ||
			stage.mode != witness.mode ||
			sameDocumentArtifactPayload(stage, witness) ||
			os.SameFile(stage.info, witness.info) ||
			hasBackup && os.SameFile(stage.info, backup.info) {
			return true, publicationConflict(
				"terminal untouched stage is not an independent published value",
				nil,
			)
		}
	}
	if hasNew {
		if newInstall.info == nil ||
			!sameDocumentArtifactPayload(newInstall, stage) ||
			!os.SameFile(newInstall.info, stage.info) {
			return true, publicationConflict(
				"terminal untouched new-install token is detached from stage",
				nil,
			)
		}
	}
	if hasStage {
		if manifest, ok := reconstructTerminalDocumentManifest(group); ok {
			group.manifest = manifest
		}
	}
	group.proofKind = "witness"
	group.leafState = documentLeafOriginal
	group.manifest.Mode = uint32(witness.mode)
	group.manifest.OriginalSize = uint64(witness.size)
	group.manifest.OriginalSHA256 = hex.EncodeToString(witness.digest[:])
	group.terminalPlan = documentV2TerminalCleanupPlan{
		form:      documentV2TerminalUntouched,
		proofKind: "witness",
		order:     []string{"new-install", "stage", "backup", "witness"},
	}
	return true, nil
}

func planDocumentV2TerminalRestored(
	group *documentRecoveryGroup,
) (bool, error) {
	anchorKind := documentRollbackAnchorProtocolKind(0)
	anchor, hasAnchor := group.artifacts[anchorKind]
	if !hasAnchor ||
		anchor.info == nil ||
		group.leafSpec.info == nil ||
		!os.SameFile(anchor.info, group.leafSpec.info) ||
		!sameDocumentArtifactPayload(anchor, group.leafSpec) {
		return false, nil
	}
	for kind := range group.artifacts {
		switch kind {
		case "witness", "backup", "stage", "new-install", "claim",
			"discard", anchorKind:
		default:
			return true, publicationConflict(
				"terminal restored document has forbidden evidence",
				nil,
			)
		}
	}
	witness, hasWitness := group.artifacts["witness"]
	backup, hasBackup := group.artifacts["backup"]
	stage, hasStage := group.artifacts["stage"]
	newInstall, hasNew := group.artifacts["new-install"]
	claim, hasClaim := group.artifacts["claim"]
	discard, hasDiscard := group.artifacts["discard"]
	basePrefix := hasWitness && hasBackup && hasStage
	validPrefix := len(group.artifacts) == 1 ||
		hasStage && !hasWitness && !hasBackup && !hasClaim &&
			!hasNew && !hasDiscard && len(group.artifacts) == 2 ||
		hasWitness && hasStage && !hasBackup && !hasClaim &&
			!hasNew && !hasDiscard && len(group.artifacts) == 3 ||
		basePrefix && !hasClaim && !hasNew && !hasDiscard &&
			len(group.artifacts) == 4 ||
		basePrefix && hasClaim && !hasNew && !hasDiscard &&
			len(group.artifacts) == 5 ||
		basePrefix && hasClaim && hasNew && !hasDiscard &&
			len(group.artifacts) == 6 ||
		basePrefix && hasClaim && !hasNew && hasDiscard &&
			len(group.artifacts) == 6
	if !validPrefix || hasNew && hasDiscard {
		return true, publicationConflict(
			"terminal restored document is not a reachable cleanup prefix",
			nil,
		)
	}
	if hasWitness {
		if witness.info == nil ||
			!sameDocumentArtifactPayload(witness, anchor) ||
			os.SameFile(witness.info, anchor.info) {
			return true, publicationConflict(
				"terminal restored witness is not an independent original",
				nil,
			)
		}
	}
	if hasBackup {
		if backup.info == nil ||
			!sameDocumentArtifactPayload(backup, anchor) ||
			os.SameFile(backup.info, anchor.info) ||
			os.SameFile(backup.info, witness.info) {
			return true, publicationConflict(
				"terminal restored backup is not an independent original copy",
				nil,
			)
		}
	}
	if hasStage {
		if stage.info == nil ||
			stage.mode != anchor.mode ||
			sameDocumentArtifactPayload(stage, anchor) ||
			os.SameFile(stage.info, anchor.info) ||
			hasWitness && os.SameFile(stage.info, witness.info) ||
			hasBackup && os.SameFile(stage.info, backup.info) {
			return true, publicationConflict(
				"terminal restored stage is not an independent published value",
				nil,
			)
		}
	}
	if hasClaim {
		if claim.info == nil ||
			!sameDocumentArtifactPayload(claim, witness) ||
			!os.SameFile(claim.info, witness.info) {
			return true, publicationConflict(
				"terminal restored claim is detached from witness",
				nil,
			)
		}
		manifest, ok := reconstructTerminalDocumentManifest(group)
		if !ok {
			return true, publicationConflict(
				"terminal restored claim lacks reconstructable manifest",
				nil,
			)
		}
		canonical, err := json.Marshal(manifest)
		if err != nil {
			return true, err
		}
		expected := documentArtifactName("claim", documentManifestDigest(canonical))
		if group.protocolNames["claim"] != expected {
			return true, publicationConflict(
				"terminal restored claim is not bound to reconstructed manifest",
				nil,
			)
		}
		group.manifest = manifest
	} else if hasWitness && hasStage {
		if manifest, ok := reconstructTerminalDocumentManifest(group); ok {
			group.manifest = manifest
		}
	}
	if hasNew {
		if newInstall.info == nil ||
			!sameDocumentArtifactPayload(newInstall, stage) ||
			!os.SameFile(newInstall.info, stage.info) {
			return true, publicationConflict(
				"terminal pre-new restored token is detached from stage",
				nil,
			)
		}
	}
	if hasDiscard {
		if discard.info == nil ||
			!sameDocumentArtifactPayload(discard, stage) ||
			!os.SameFile(discard.info, stage.info) {
			return true, publicationConflict(
				"terminal published restored discard is detached from stage",
				nil,
			)
		}
	}
	form := documentV2TerminalRestoredPrefix
	switch {
	case hasNew:
		form = documentV2TerminalPreNewRestored
	case hasDiscard:
		form = documentV2TerminalPublishedRestored
	}
	group.proofKind = anchorKind
	group.leafState = documentLeafOriginal
	group.manifest.Mode = uint32(anchor.mode)
	group.manifest.OriginalSize = uint64(anchor.size)
	group.manifest.OriginalSHA256 = hex.EncodeToString(anchor.digest[:])
	group.terminalPlan = documentV2TerminalCleanupPlan{
		form:      form,
		proofKind: anchorKind,
		order: []string{
			"new-install",
			"discard",
			"claim",
			"backup",
			"witness",
			"stage",
			anchorKind,
		},
	}
	return true, nil
}

func sameDocumentArtifactPayload(left, right publicationFileSpec) bool {
	return left.complete &&
		right.complete &&
		left.mode == right.mode &&
		left.size == right.size &&
		left.digest == right.digest &&
		bytes.Equal(left.data, right.data)
}

func applyTerminalDocumentRecovery(
	ctx context.Context,
	group *documentRecoveryGroup,
	hooks documentPublishHooks,
) error {
	var order []string
	proofKind := group.proofKind
	if group.terminalPlan.form != documentV2TerminalInvalid {
		order = append([]string(nil), group.terminalPlan.order...)
		proofKind = group.terminalPlan.proofKind
	} else {
		switch group.proofKind {
		case "witness":
			order = []string{"new-install", "stage", "backup", "witness"}
		case "stage":
			order = []string{"stage"}
		default:
			order = []string{group.proofKind}
		}
	}
	if err := cleanupDocumentRecoveryGroupWithProof(
		ctx,
		group,
		hooks,
		order,
		proofKind,
	); err != nil {
		return err
	}
	group.completed = true
	return revalidateDocumentRecoveryGroup(context.WithoutCancel(ctx), group)
}

func cleanupDocumentRecoveryGroupWithProof(
	ctx context.Context,
	group *documentRecoveryGroup,
	hooks documentPublishHooks,
	order []string,
	proofKind string,
) error {
	proof, present := group.artifacts[proofKind]
	if !present || proof.info == nil || !proof.complete {
		return publicationConflict("document cleanup identity proof is unavailable", nil)
	}
	_, leaf := pathSplitDocumentTarget(group.manifest.Target)
	verifyProof := func() error {
		if err := verifyPublicationFile(
			context.WithoutCancel(ctx),
			group.parent,
			group.names[proofKind],
			proof,
			true,
		); err != nil {
			return publicationConflict("document cleanup proof changed", err)
		}
		target, err := capturePublicationFile(context.WithoutCancel(ctx), group.parent, leaf)
		if err != nil ||
			target.info == nil ||
			!os.SameFile(target.info, proof.info) ||
			!sameDocumentArtifactPayload(target, proof) {
			return publicationConflict("document cleanup target detached from proof", err)
		}
		return nil
	}
	if err := verifyProof(); err != nil {
		return err
	}
	for _, kind := range order {
		spec, artifactPresent := group.artifacts[kind]
		if !artifactPresent {
			continue
		}
		if kind != proofKind {
			if err := verifyProof(); err != nil {
				return err
			}
		}
		removed, err := guardedRemovePublicationFileAs(
			context.WithoutCancel(ctx),
			group.parent,
			group.names[kind],
			group.protocolNames[kind],
			spec,
			documentRecoveryRemovalHooks(hooks, group, kind),
		)
		if removed {
			group.inventory.remove(group, kind)
			delete(group.artifacts, kind)
			delete(group.names, kind)
			delete(group.protocolNames, kind)
		}
		if err != nil {
			return err
		}
		if kind != proofKind {
			if err := verifyProof(); err != nil {
				return err
			}
			continue
		}
		target, captureErr := capturePublicationFile(context.WithoutCancel(ctx), group.parent, leaf)
		if captureErr != nil ||
			target.info == nil ||
			!os.SameFile(target.info, proof.info) ||
			!sameDocumentArtifactPayload(target, proof) {
			return publicationConflict(
				"document target changed across final proof cleanup",
				captureErr,
			)
		}
		group.leafSpec = target
	}
	if _, remains := group.artifacts[proofKind]; remains {
		return publicationConflict("document cleanup retained final proof", nil)
	}
	return nil
}

func documentRecoveryRemovalHooks(
	hooks documentPublishHooks,
	group *documentRecoveryGroup,
	kind string,
) publicationBarrierHooks {
	name := group.names[kind]
	return publicationBarrierHooks{
		fileSync:      hooks.fileSync,
		directorySync: hooks.directorySync,
		validateScope: documentRecoveryScopeValidatorAs(
			group,
			kind,
			name,
			group.protocolNames[kind],
			"",
		),
		beforeRemove: func() error {
			if hooks.beforeCleanup == nil {
				return nil
			}
			return hooks.beforeCleanup(group.parent, kind, name)
		},
		afterRemove: func() error {
			if hooks.afterCleanup == nil {
				return nil
			}
			return hooks.afterCleanup(group.parent, kind, name)
		},
	}
}

func pathSplitDocumentTarget(target string) (string, string) {
	for index := len(target) - 1; index >= 0; index-- {
		if target[index] == '/' {
			return target[:index], target[index+1:]
		}
	}
	return "", target
}
