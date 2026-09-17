package bundle

import (
	"context"
	"os"
)

type documentRecoveryInventory struct {
	root     *os.Root
	expected map[string]publicationNamespaceDiscovery
}

func newDocumentRecoveryInventory(
	root *os.Root,
	snapshot []publicationNamespaceDiscovery,
) *documentRecoveryInventory {
	expected := make(map[string]publicationNamespaceDiscovery, len(snapshot))
	for _, discovery := range snapshot {
		expected[discovery.relative] = discovery
	}
	return &documentRecoveryInventory{root: root, expected: expected}
}

func (inventory *documentRecoveryInventory) add(
	group *documentRecoveryGroup,
	kind string,
) {
	if inventory == nil {
		return
	}
	name := group.names[kind]
	relative := pathJoin(group.directory, name)
	spec := group.artifacts[kind]
	inventory.expected[relative] = publicationNamespaceDiscovery{
		namespace:    "document-v2",
		kind:         kind,
		directory:    group.directory,
		name:         name,
		protocolName: group.protocolNames[kind],
		relative:     relative,
		parentInfo:   group.parentInfo,
		info:         spec.info,
	}
}

func (inventory *documentRecoveryInventory) remove(
	group *documentRecoveryGroup,
	kind string,
) {
	if inventory == nil {
		return
	}
	delete(inventory.expected, pathJoin(group.directory, group.names[kind]))
}

func (inventory *documentRecoveryInventory) revalidate(ctx context.Context) error {
	if inventory == nil {
		return nil
	}
	current, err := discoverPublicationNamespaces(
		ctx,
		inventory.root,
		[]publicationNamespaceRule{
			documentNamespaceRule(),
			privatePublicationNamespaceRule(),
		},
	)
	if err != nil {
		return err
	}
	if len(current) != len(inventory.expected) {
		return publicationConflict("document recovery namespace changed", nil)
	}
	for _, discovery := range current {
		expected, present := inventory.expected[discovery.relative]
		if !present ||
			expected.directory != discovery.directory ||
			expected.name != discovery.name ||
			!os.SameFile(expected.parentInfo, discovery.parentInfo) ||
			!samePublicationEntryObservation(expected.info, discovery.info) {
			return publicationConflict("document recovery namespace observation changed", nil)
		}
	}
	return nil
}
