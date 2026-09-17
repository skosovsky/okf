package okfcli

import (
	"fmt"
	"io"
)

type checkedWriter struct {
	writer io.Writer
	err    error
}

func (writer *checkedWriter) Write(value []byte) (int, error) {
	if writer.err != nil {
		return 0, writer.err
	}
	written, err := writer.writer.Write(value)
	if err == nil && written != len(value) {
		err = io.ErrShortWrite
	}
	if err != nil {
		writer.err = fmt.Errorf("write stdout: %w", err)
		return written, writer.err
	}
	return written, nil
}

func (writer *checkedWriter) Err() error {
	return writer.err
}
