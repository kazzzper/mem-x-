// Package parser turns the raw token stream from the wire into a typed
// Command. It is deliberately thin: protocol mechanics live in internal/resp
// and semantics live in internal/command. It exists so token-level invariants
// (name normalization, empty-command handling) have one tested home.
package parser

import (
	"bytes"
	"errors"
)

// Command is a parsed client command. Name is lowercased; Args holds the raw
// argument tokens exactly as received.
type Command struct {
	Name string
	Args [][]byte
}

// ErrEmptyCommand is returned when there are no tokens to parse.
var ErrEmptyCommand = errors.New("empty command")

// Parse builds a Command from raw tokens (name first). It lowercases the name
// so dispatch is case-insensitive, matching Redis.
func Parse(tokens [][]byte) (Command, error) {
	if len(tokens) == 0 {
		return Command{}, ErrEmptyCommand
	}
	name := string(bytes.ToLower(tokens[0]))
	if name == "" {
		return Command{}, ErrEmptyCommand
	}
	cmd := Command{Name: name}
	if len(tokens) > 1 {
		cmd.Args = tokens[1:]
	}
	return cmd, nil
}
