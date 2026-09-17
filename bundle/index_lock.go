package bundle

import (
	"errors"
	"fmt"
	"os"
	"sync"
)

var (
	// ErrPublicationOwnershipConflict reports that another cooperative
	// publisher owns the same physical bundle root.
	ErrPublicationOwnershipConflict = errors.New("publication ownership conflict")

	// ErrPublicationCapabilityUnsupported reports that this platform cannot
	// provide all ownership and durability guarantees required for publication.
	ErrPublicationCapabilityUnsupported = errors.New("publication capability unsupported")
)

type localPublicationLock struct{}

var localPublicationLocks = struct {
	sync.Mutex
	held map[string]*localPublicationLock
}{
	held: make(map[string]*localPublicationLock),
}

func acquirePublicationLock(root *os.Root) (release func(), err error) {
	identity, err := physicalPublicationRootIdentity(root)
	if err != nil {
		return nil, fmt.Errorf("acquire publication lock: %w", err)
	}
	if err := syncPublicationDirectoryPlatform(root); err != nil {
		return nil, fmt.Errorf(
			"%w: durable root directory sync probe: %v",
			ErrPublicationCapabilityUnsupported,
			err,
		)
	}
	localRelease, err := tryAcquireLocalPublicationLock(identity)
	if err != nil {
		return nil, err
	}
	crossProcessRelease, err := tryAcquireCrossProcessPublicationLock(root, identity)
	if err != nil {
		localRelease()
		return nil, err
	}
	var once sync.Once
	return func() {
		once.Do(func() {
			crossProcessRelease()
			localRelease()
		})
	}, nil
}

func tryAcquireLocalPublicationLock(identity string) (func(), error) {
	localPublicationLocks.Lock()
	if _, exists := localPublicationLocks.held[identity]; exists {
		localPublicationLocks.Unlock()
		return nil, fmt.Errorf(
			"%w: physical bundle root %q is already owned in this process",
			ErrPublicationOwnershipConflict,
			identity,
		)
	}
	ownership := &localPublicationLock{}
	localPublicationLocks.held[identity] = ownership
	localPublicationLocks.Unlock()

	var once sync.Once
	return func() {
		once.Do(func() {
			localPublicationLocks.Lock()
			if localPublicationLocks.held[identity] == ownership {
				delete(localPublicationLocks.held, identity)
			}
			localPublicationLocks.Unlock()
		})
	}, nil
}
