// Package protocol defines the bounded binary guest/daemon exec protocol.
// It deliberately avoids whitespace-delimited argv and environment messages.
package protocol

import (
	"encoding/binary"
	"fmt"
	"io"
)

const (
	Magic       = "SBX1"
	Version     = uint32(1)
	MaxFrame    = 1 << 20
	MaxString   = 64 << 10
	MaxVector   = 256
	TypeExecReq = byte(1)
	TypeExecRes = byte(2)
	TypeOpReq   = byte(3)
	TypeOpRes   = byte(4)

	OpRun     = byte(1)
	OpRead    = byte(2)
	OpConfirm = byte(3)
)

type ExecRequest struct {
	Path, Dir string
	Argv      []string
	Env       []string
}
type ExecResult struct {
	Allowed bool
	Path    string
	Argv    []string
	Env     []string
	Message string
}
type Operation struct {
	ID      uint64
	Kind    byte
	Path    string
	Args    []string
	Dir     string
	Env     []string
	Prompt  string
	Capture bool
}
type OperationResult struct {
	ID       uint64
	OK       bool
	ExitCode int32
	Data     []byte
	Message  string
}

func WriteMagic(w io.Writer) error { _, err := io.WriteString(w, Magic); return err }
func ReadMagic(r io.Reader) error {
	var got [4]byte
	if _, err := io.ReadFull(r, got[:]); err != nil {
		return err
	}
	if string(got[:]) != Magic {
		return fmt.Errorf("sandbox protocol magic mismatch")
	}
	return nil
}
func WriteFrame(w io.Writer, typ byte, payload []byte) error {
	if len(payload)+1 > MaxFrame {
		return fmt.Errorf("sandbox protocol frame too large")
	}
	var hdr [4]byte
	binary.BigEndian.PutUint32(hdr[:], uint32(len(payload)+1))
	if _, err := w.Write(hdr[:]); err != nil {
		return err
	}
	if _, err := w.Write([]byte{typ}); err != nil {
		return err
	}
	_, err := w.Write(payload)
	return err
}
func ReadFrame(r io.Reader) (byte, []byte, error) {
	var hdr [4]byte
	if _, err := io.ReadFull(r, hdr[:]); err != nil {
		return 0, nil, err
	}
	n := binary.BigEndian.Uint32(hdr[:])
	if n == 0 || n > MaxFrame {
		return 0, nil, fmt.Errorf("invalid sandbox protocol frame length %d", n)
	}
	body := make([]byte, n)
	if _, err := io.ReadFull(r, body); err != nil {
		return 0, nil, err
	}
	return body[0], body[1:], nil
}

func EncodeExecRequest(v ExecRequest) ([]byte, error) {
	e := encoder{}
	e.str(v.Path)
	e.str(v.Dir)
	e.vec(v.Argv)
	e.vec(v.Env)
	return e.bytes(), e.err
}
func DecodeExecRequest(b []byte) (ExecRequest, error) {
	d := decoder{b: b}
	v := ExecRequest{Path: d.str(), Dir: d.str(), Argv: d.vec(), Env: d.vec()}
	return v, d.done()
}
func EncodeExecResult(v ExecResult) ([]byte, error) {
	e := encoder{}
	e.bool(v.Allowed)
	e.str(v.Path)
	e.vec(v.Argv)
	e.vec(v.Env)
	e.str(v.Message)
	return e.bytes(), e.err
}
func DecodeExecResult(b []byte) (ExecResult, error) {
	d := decoder{b: b}
	v := ExecResult{Allowed: d.bool(), Path: d.str(), Argv: d.vec(), Env: d.vec(), Message: d.str()}
	return v, d.done()
}
func EncodeOperation(v Operation) ([]byte, error) {
	e := encoder{}
	e.u64(v.ID)
	e.u8(v.Kind)
	e.str(v.Path)
	e.vec(v.Args)
	e.str(v.Dir)
	e.vec(v.Env)
	e.str(v.Prompt)
	e.bool(v.Capture)
	return e.bytes(), e.err
}
func DecodeOperation(b []byte) (Operation, error) {
	d := decoder{b: b}
	v := Operation{ID: d.u64(), Kind: d.u8(), Path: d.str(), Args: d.vec(), Dir: d.str(), Env: d.vec(), Prompt: d.str(), Capture: d.bool()}
	return v, d.done()
}
func EncodeOperationResult(v OperationResult) ([]byte, error) {
	e := encoder{}
	e.u64(v.ID)
	e.bool(v.OK)
	e.i32(v.ExitCode)
	e.blob(v.Data)
	e.str(v.Message)
	return e.bytes(), e.err
}
func DecodeOperationResult(b []byte) (OperationResult, error) {
	d := decoder{b: b}
	v := OperationResult{ID: d.u64(), OK: d.bool(), ExitCode: d.i32(), Data: d.blob(), Message: d.str()}
	return v, d.done()
}

type encoder struct {
	b   []byte
	err error
}

func (e *encoder) u8(v byte) { e.b = append(e.b, v) }
func (e *encoder) bool(v bool) {
	if v {
		e.u8(1)
	} else {
		e.u8(0)
	}
}
func (e *encoder) u32(v uint32) {
	var b [4]byte
	binary.BigEndian.PutUint32(b[:], v)
	e.b = append(e.b, b[:]...)
}
func (e *encoder) u64(v uint64) {
	var b [8]byte
	binary.BigEndian.PutUint64(b[:], v)
	e.b = append(e.b, b[:]...)
}
func (e *encoder) i32(v int32) { e.u32(uint32(v)) }
func (e *encoder) blob(v []byte) {
	if len(v) > MaxString {
		e.err = fmt.Errorf("sandbox protocol value too long")
		return
	}
	e.u32(uint32(len(v)))
	e.b = append(e.b, v...)
}
func (e *encoder) str(v string) { e.blob([]byte(v)) }
func (e *encoder) vec(v []string) {
	if len(v) > MaxVector {
		e.err = fmt.Errorf("sandbox protocol vector too long")
		return
	}
	e.u32(uint32(len(v)))
	for _, s := range v {
		e.str(s)
	}
}
func (e *encoder) bytes() []byte { return e.b }

type decoder struct {
	b   []byte
	off int
	err error
}

func (d *decoder) take(n int) []byte {
	if d.err != nil || n < 0 || len(d.b)-d.off < n {
		d.err = fmt.Errorf("truncated sandbox protocol value")
		return nil
	}
	v := d.b[d.off : d.off+n]
	d.off += n
	return v
}
func (d *decoder) u8() byte {
	v := d.take(1)
	if len(v) == 0 {
		return 0
	}
	return v[0]
}
func (d *decoder) bool() bool {
	v := d.u8()
	if v > 1 {
		d.err = fmt.Errorf("invalid sandbox protocol boolean")
	}
	return v == 1
}
func (d *decoder) u32() uint32 {
	v := d.take(4)
	if len(v) < 4 {
		return 0
	}
	return binary.BigEndian.Uint32(v)
}
func (d *decoder) u64() uint64 {
	v := d.take(8)
	if len(v) < 8 {
		return 0
	}
	return binary.BigEndian.Uint64(v)
}
func (d *decoder) i32() int32 { return int32(d.u32()) }
func (d *decoder) blob() []byte {
	n := d.u32()
	if n > MaxString {
		d.err = fmt.Errorf("sandbox protocol value too long")
		return nil
	}
	return append([]byte(nil), d.take(int(n))...)
}
func (d *decoder) str() string { return string(d.blob()) }
func (d *decoder) vec() []string {
	n := d.u32()
	if n > MaxVector {
		d.err = fmt.Errorf("sandbox protocol vector too long")
		return nil
	}
	out := make([]string, 0, n)
	for i := uint32(0); i < n; i++ {
		out = append(out, d.str())
	}
	return out
}
func (d *decoder) done() error {
	if d.err != nil {
		return d.err
	}
	if d.off != len(d.b) {
		return fmt.Errorf("trailing sandbox protocol data")
	}
	return nil
}
