package transport

import "errors"

var (
	ErrCircuitOpen      = errors.New("upstream circuit open")
	ErrNoHealthyTargets = errors.New("no healthy targets")
)
