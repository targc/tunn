package proto

import (
	"encoding/binary"
	"errors"
	"fmt"
)

const (
	MsgOpenStream  byte = 0x01
	MsgStreamReady byte = 0x02
	MsgData        byte = 0x03
	MsgCloseStream byte = 0x04
	MsgStreamError byte = 0x05

	HeaderSize = 5 // 1 byte type + 4 bytes stream ID
)

var ErrFrameTooShort = errors.New("frame too short")

type Frame struct {
	Type     byte
	StreamID uint32
	Payload  []byte
}

func EncodeFrame(f *Frame) []byte {
	buf := make([]byte, HeaderSize+len(f.Payload))
	buf[0] = f.Type
	binary.BigEndian.PutUint32(buf[1:5], f.StreamID)
	copy(buf[HeaderSize:], f.Payload)
	return buf
}

func DecodeFrame(data []byte) (*Frame, error) {
	if len(data) < HeaderSize {
		return nil, fmt.Errorf("%w: got %d bytes", ErrFrameTooShort, len(data))
	}
	return &Frame{
		Type:     data[0],
		StreamID: binary.BigEndian.Uint32(data[1:5]),
		Payload:  data[HeaderSize:],
	}, nil
}
