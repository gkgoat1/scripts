package core

import (
	"os"
)

// Context carries runtime state for an interposed command invocation.
type Context struct {
	Name       string
	Args       []string
	RealBinary string
	Dir        string
	Env        []string
}

// NewContext builds a Context from the current process environment.
func NewContext(name string, args []string, realBinary string) *Context {
	dir, _ := os.Getwd()
	return &Context{
		Name:       name,
		Args:       args,
		RealBinary: realBinary,
		Dir:        dir,
		Env:        os.Environ(),
	}
}
