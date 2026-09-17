package bundle

import "errors"

type indexBatchV3IdentityRole uint8

const (
	indexBatchV3IdentityStage indexBatchV3IdentityRole = iota + 1
	indexBatchV3IdentityNewInstall
	indexBatchV3IdentityAnchor
	indexBatchV3IdentityRestoreInstall
	indexBatchV3IdentityDiscard
	indexBatchV3IdentityBackup
	indexBatchV3IdentityClaim
	indexBatchV3IdentityWitness
)

type indexBatchV3IdentityRelation struct {
	left     indexBatchV3IdentityRole
	right    indexBatchV3IdentityRole
	sameFile bool
}

func validateIndexBatchV3IdentityRelations(relations []indexBatchV3IdentityRelation) error {
	for _, relation := range relations {
		if !relation.sameFile {
			continue
		}
		if indexBatchV3IdentityAliasAllowed(relation.left, relation.right) {
			continue
		}
		return errors.New("index batch v3 contains a forbidden artifact identity alias")
	}
	return nil
}

func indexBatchV3IdentityAliasAllowed(left, right indexBatchV3IdentityRole) bool {
	switch {
	case left == indexBatchV3IdentityStage && right == indexBatchV3IdentityNewInstall,
		left == indexBatchV3IdentityNewInstall && right == indexBatchV3IdentityStage:
		return true
	case left == indexBatchV3IdentityAnchor && right == indexBatchV3IdentityRestoreInstall,
		left == indexBatchV3IdentityRestoreInstall && right == indexBatchV3IdentityAnchor:
		return true
	case left == indexBatchV3IdentityStage && right == indexBatchV3IdentityDiscard,
		left == indexBatchV3IdentityDiscard && right == indexBatchV3IdentityStage:
		return true
	default:
		return false
	}
}
