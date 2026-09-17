//go:build (!darwin && !linux) || ios || android

package fs

import "testing"

func TestDescriptorBarrierRejectsSwappedRenameAndRemoveParents(t *testing.T) {
	// Arrange. Descriptor-backed rename and remove barriers are unsupported on
	// this platform, so the native adversarial owner cannot execute.

	// Act and assert. Keep the authoritative owner registry closed while making
	// the unsupported contract explicit and mutation-free.
	t.Skip("descriptor-backed rename and remove barriers are unsupported on this platform")
}
