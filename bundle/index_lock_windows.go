//go:build windows

package bundle

import (
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"fmt"
	"os"
	"runtime"
	"sync"

	"golang.org/x/sys/windows"
)

const publicationWindowsMutexNamespace = `Global\OKF.Publication.`

type windowsPublicationLockAcquisition struct {
	release func()
	err     error
}

func physicalPublicationRootIdentity(root *os.Root) (identity string, err error) {
	if root == nil {
		return "", errors.New("identify publication root: nil root")
	}

	directory, err := root.Open(".")
	if err != nil {
		return "", fmt.Errorf("identify publication root: open pinned directory: %w", err)
	}
	defer func() {
		err = errors.Join(err, directory.Close())
	}()

	var information windows.ByHandleFileInformation
	if err := windows.GetFileInformationByHandle(
		windows.Handle(directory.Fd()),
		&information,
	); err != nil {
		return "", fmt.Errorf("identify publication root: query pinned directory handle: %w", err)
	}

	return fmt.Sprintf(
		"volume:%08x:file:%08x%08x",
		information.VolumeSerialNumber,
		information.FileIndexHigh,
		information.FileIndexLow,
	), nil
}

func tryAcquireCrossProcessPublicationLock(root *os.Root, identity string) (release func(), err error) {
	if identity == "" {
		return nil, errors.New("acquire publication lock: empty root identity")
	}
	currentIdentity, err := physicalPublicationRootIdentity(root)
	if err != nil {
		return nil, err
	}
	if currentIdentity != identity {
		return nil, fmt.Errorf(
			"physical bundle root changed while acquiring publication lock: %q != %q",
			currentIdentity,
			identity,
		)
	}

	digest := sha256.Sum256([]byte(identity))
	name, err := windows.UTF16PtrFromString(
		publicationWindowsMutexNamespace + hex.EncodeToString(digest[:]),
	)
	if err != nil {
		return nil, fmt.Errorf("acquire publication lock: encode mutex name: %w", err)
	}

	acquisition := make(chan windowsPublicationLockAcquisition, 1)
	go acquireWindowsPublicationMutex(name, acquisition)
	result := <-acquisition
	return result.release, result.err
}

func acquireWindowsPublicationMutex(
	name *uint16,
	acquisition chan<- windowsPublicationLockAcquisition,
) {
	mutex, createErr := windows.CreateMutex(nil, false, name)
	if createErr != nil && !errors.Is(createErr, windows.ERROR_ALREADY_EXISTS) {
		if mutex != 0 {
			_ = windows.CloseHandle(mutex)
		}
		acquisition <- windowsPublicationLockAcquisition{
			err: fmt.Errorf("acquire publication lock: create mutex: %w", createErr),
		}
		return
	}
	if mutex == 0 {
		acquisition <- windowsPublicationLockAcquisition{
			err: errors.New("acquire publication lock: create mutex returned an invalid handle"),
		}
		return
	}

	// Win32 mutex ownership belongs to the acquiring OS thread. A dedicated
	// goroutine retains that thread until release, so callers may invoke the
	// returned function from any goroutine without violating mutex ownership.
	runtime.LockOSThread()
	waitResult, waitErr := windows.WaitForSingleObject(mutex, 0)
	if waitErr != nil {
		_ = windows.CloseHandle(mutex)
		runtime.UnlockOSThread()
		acquisition <- windowsPublicationLockAcquisition{
			err: fmt.Errorf("acquire publication lock: wait for mutex: %w", waitErr),
		}
		return
	}

	switch waitResult {
	case windows.WAIT_OBJECT_0, windows.WAIT_ABANDONED:
		releaseRequested := make(chan struct{})
		released := make(chan struct{})
		var once sync.Once
		acquisition <- windowsPublicationLockAcquisition{release: func() {
			once.Do(func() {
				close(releaseRequested)
			})
			<-released
		}}
		<-releaseRequested
		_ = windows.ReleaseMutex(mutex)
		_ = windows.CloseHandle(mutex)
		close(released)
		runtime.UnlockOSThread()
	case uint32(windows.WAIT_TIMEOUT):
		_ = windows.CloseHandle(mutex)
		runtime.UnlockOSThread()
		acquisition <- windowsPublicationLockAcquisition{
			err: fmt.Errorf(
				"%w: another process owns the publication lock",
				ErrPublicationOwnershipConflict,
			),
		}
	default:
		_ = windows.CloseHandle(mutex)
		runtime.UnlockOSThread()
		acquisition <- windowsPublicationLockAcquisition{
			err: fmt.Errorf(
				"acquire publication lock: unexpected wait result %#x",
				waitResult,
			),
		}
	}
}
