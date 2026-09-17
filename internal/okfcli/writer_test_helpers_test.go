package okfcli

import (
	"bytes"
)

type scriptedWriter struct {
	bytes.Buffer
	short           bool
	err             error
	session         *recordingDocumentSession
	writesBeforeEnd int
	writeCalls      int
	events          *[]string
}

func (writer *scriptedWriter) Write(value []byte) (int, error) {
	writer.writeCalls++
	if writer.session != nil && writer.session.closeCalls == 0 {
		writer.writesBeforeEnd++
	}
	if writer.events != nil {
		*writer.events = append(*writer.events, "stdout")
	}
	if writer.short {
		return len(value) / 2, nil
	}
	if writer.err != nil {
		return 0, writer.err
	}
	return writer.Buffer.Write(value)
}
