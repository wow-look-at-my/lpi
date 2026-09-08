package estimate

import (
	"io"

	"github.com/wow-look-at-my/lpi/internal/linescan"
)

// Scanner splits a stream into lines, truncating overlong ones rather than failing.
type Scanner = linescan.Scanner

// NewScanner returns a Scanner over r, for feeding a live stream to an Estimator.
func NewScanner(r io.Reader) *Scanner { return linescan.NewScanner(r) }
