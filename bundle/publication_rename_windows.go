//go:build windows

package bundle

import (
	"errors"
	"fmt"
	"os"
	"unsafe"

	"golang.org/x/sys/windows"
)

type publicationFileRenameInfo struct {
	ReplaceIfExists uint32
	RootDirectory   windows.Handle
	FileNameLength  uint32
	FileName        [1]uint16
}

func renamePublicationNoReplace(parent *os.Root, oldName, newName string) error {
	if err := validatePublicationRenameLeaf(oldName, newName); err != nil {
		return err
	}
	if parent == nil {
		return &os.LinkError{
			Op:  "SetFileInformationByHandle",
			Old: oldName,
			New: newName,
			Err: fmt.Errorf("%w: nil publication root", os.ErrInvalid),
		}
	}
	root, err := parent.Open(".")
	if err != nil {
		return &os.LinkError{
			Op:  "SetFileInformationByHandle",
			Old: oldName,
			New: newName,
			Err: err,
		}
	}
	defer root.Close()
	rootHandle := windows.Handle(root.Fd())

	source, err := openPublicationRenameSource(rootHandle, oldName)
	if err != nil {
		return &os.LinkError{
			Op:  "SetFileInformationByHandle",
			Old: oldName,
			New: newName,
			Err: err,
		}
	}
	defer windows.CloseHandle(source)

	name, err := windows.UTF16FromString(newName)
	if err != nil {
		return &os.LinkError{
			Op:  "SetFileInformationByHandle",
			Old: oldName,
			New: newName,
			Err: err,
		}
	}
	name = name[:len(name)-1]

	var layout publicationFileRenameInfo
	nameOffset := unsafe.Offsetof(layout.FileName)
	buffer := make([]byte, nameOffset+uintptr(len(name))*unsafe.Sizeof(name[0]))
	info := (*publicationFileRenameInfo)(unsafe.Pointer(&buffer[0]))
	info.RootDirectory = rootHandle
	info.FileNameLength = uint32(len(name) * 2)
	copy(unsafe.Slice(&info.FileName[0], len(name)), name)

	err = windows.SetFileInformationByHandle(
		source,
		windows.FileRenameInfo,
		&buffer[0],
		uint32(len(buffer)),
	)
	if err == nil {
		return nil
	}
	if errors.Is(err, windows.ERROR_NOT_SUPPORTED) ||
		errors.Is(err, windows.ERROR_INVALID_FUNCTION) ||
		errors.Is(err, windows.ERROR_CALL_NOT_IMPLEMENTED) ||
		errors.Is(err, windows.ERROR_INVALID_PARAMETER) ||
		errors.Is(err, windows.ERROR_INVALID_LEVEL) {
		err = fmt.Errorf("%w: %w", errPublicationNoReplaceUnsupported, err)
	}
	return &os.LinkError{
		Op:  "SetFileInformationByHandle",
		Old: oldName,
		New: newName,
		Err: err,
	}
}

func openPublicationRenameSource(
	root windows.Handle,
	name string,
) (windows.Handle, error) {
	objectName, err := windows.NewNTUnicodeString(name)
	if err != nil {
		return windows.InvalidHandle, err
	}
	attributes := windows.OBJECT_ATTRIBUTES{
		Length:        uint32(unsafe.Sizeof(windows.OBJECT_ATTRIBUTES{})),
		RootDirectory: root,
		ObjectName:    objectName,
	}
	var source windows.Handle
	err = windows.NtCreateFile(
		&source,
		windows.DELETE|windows.SYNCHRONIZE,
		&attributes,
		&windows.IO_STATUS_BLOCK{},
		nil,
		windows.FILE_ATTRIBUTE_NORMAL,
		windows.FILE_SHARE_READ|windows.FILE_SHARE_WRITE|windows.FILE_SHARE_DELETE,
		windows.FILE_OPEN,
		windows.FILE_OPEN_FOR_BACKUP_INTENT|
			windows.FILE_OPEN_REPARSE_POINT|
			windows.FILE_SYNCHRONOUS_IO_NONALERT,
		0,
		0,
	)
	if err != nil {
		if status, ok := err.(windows.NTStatus); ok {
			err = status.Errno()
		}
		return windows.InvalidHandle, err
	}
	return source, nil
}
