package eval

import (
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/stretchr/testify/require"

	"github.com/wow-look-at-my/lpi/internal/model"
)

// A synthetic corpus: shared core lines in order, plus lines peculiar to each
// run, as a build prints when it recompiles another subset.
const (
	corpusCore = 200
	corpusGap  = 3 * time.Second
)

// runSpec describes a synthetic log.
type runSpec struct {
	name string
	// quirk is how many lines only this run prints, spread through the run.
	quirk int
	// truncate keeps only this fraction of the run, as a log cut short does.
	truncate float64
	// pace multiplies every gap, so the run takes proportionally longer.
	pace float64
}

// writeRun writes spec's log and returns its path.
func writeRun(t *testing.T, dir string, spec runSpec) string {
	t.Helper()
	pace := spec.pace
	if pace == 0 {
		pace = 1
	}
	lines := corpusCore
	if spec.truncate > 0 {
		lines = int(float64(corpusCore) * spec.truncate)
	}
	quirkEvery := corpusCore + 1
	if spec.quirk > 0 {
		quirkEvery = corpusCore / spec.quirk
	}
	var b strings.Builder
	at := base
	emit := func(text string) {
		fmt.Fprintf(&b, "%s %s\n", at.Format("2006-01-02T15:04:05Z"), text)
		at = at.Add(time.Duration(float64(corpusGap) * pace))
	}
	for i := range lines {
		emit(fmt.Sprintf("compiling core module %d of the target", i))
		if i > 0 && i%quirkEvery == 0 {
			emit(fmt.Sprintf("regenerating %s artifact %d", spec.name, i))
		}
	}
	path := filepath.Join(dir, spec.name+".log")
	require.NoError(t, os.WriteFile(path, []byte(b.String()), 0o600))
	return path
}

// corpusTarget writes a run and digests it as the eval command does.
func corpusTarget(t *testing.T, dir string, spec runSpec) Target {
	t.Helper()
	path := writeRun(t, dir, spec)
	run, err := model.DigestFileWith(path, nil)
	require.NoError(t, err)
	return Target{Path: path, Run: run}
}

// modelOf merges targets into a model, as `lpi learn` does.
func modelOf(targets ...Target) *model.Model {
	m := model.New("corpus")
	for _, t := range targets {
		m.AddRun(t.Run)
	}
	return m
}

// scoreAgainst scores a holdout against refs.
func scoreAgainst(t *testing.T, holdout Target, refs ...Target) *Result {
	t.Helper()
	r, err := Score(modelOf(refs...), holdout, nil)
	require.NoError(t, err)
	return r
}
