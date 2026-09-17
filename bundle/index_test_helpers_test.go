package bundle

import "os"

// withoutPhysicalIndexDurability keeps fault matrices deterministic and fast:
// their contract is the ordering of durability boundaries and the resulting
// recovery state. Hooks supplied by a sync-specific test are preserved.
func withoutPhysicalIndexDurability(hooks indexPublishHooks) indexPublishHooks {
	if hooks.fileSync == nil {
		hooks.fileSync = func(string, string, string, *os.File) error {
			return nil
		}
	}
	if hooks.directorySync == nil {
		hooks.directorySync = func(string, string, string, *os.Root) error {
			return nil
		}
	}
	return hooks
}

func regenerateIndexesWithTestHooks(
	root string,
	synthesize SynthesizeDescription,
	version string,
	hooks indexPublishHooks,
) ([]string, error) {
	return regenerateIndexesWithHooks(
		root,
		synthesize,
		version,
		withoutPhysicalIndexDurability(hooks),
	)
}

func regenerateIndexesForTest(root string) ([]string, error) {
	return regenerateIndexesWithTestHooks(root, nil, "", indexPublishHooks{})
}

func regenerateIndexesWithSelectorForTest(root, selector string) ([]string, error) {
	return regenerateIndexesWithTestHooks(root, DefaultSynthesizeDescription, selector, indexPublishHooks{})
}
