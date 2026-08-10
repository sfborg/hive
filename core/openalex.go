package core

import "github.com/sfborg/hive/pkg/openalex"

// ErrOpenAlexNotFound is re-exported so existing callers keep compiling
// against a stable core-package symbol.
var ErrOpenAlexNotFound = openalex.ErrNotFound

var openAlexInstance *openalex.Client

// OpenAlex returns a shared openalex.Client built from the current
// process identity's OpenAlexEmail (see CurrentIdentity, set by
// main.go after resolving flag > env > config). Cached — repeated
// calls return the same client. Change requires a process restart.
func OpenAlex() *openalex.Client {
	if openAlexInstance == nil {
		openAlexInstance = openalex.New(CurrentIdentity().OpenAlexEmail)
	}
	return openAlexInstance
}
