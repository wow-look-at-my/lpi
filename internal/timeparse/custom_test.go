package timeparse

import (
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestCompileAutoAndBuiltins(t *testing.T) {
	f, err := Compile("", "")
	require.NoError(t, err)
	assert.Nil(t, f, "an empty spec asks for detection")

	f, err = Compile("auto", "")
	require.NoError(t, err)
	assert.Nil(t, f)

	f, err = Compile("ISO8601", "")
	require.NoError(t, err)
	assert.Equal(t, "iso8601", f.Name())
	at, ok := f.Parse("2026-03-04T05:06:07Z hello")
	require.True(t, ok)
	assert.Equal(t, time.Date(2026, time.March, 4, 5, 6, 7, 0, time.UTC), at)
}

func TestCompileRejectsBadSpecs(t *testing.T) {
	_, err := Compile("nonsense", "")
	require.Error(t, err)
	assert.Contains(t, err.Error(), "unknown format")

	_, err = Compile("iso8601", "15:04:05")
	require.Error(t, err)

	_, err = Compile(`regex:(?P<time>[`, "")
	require.Error(t, err)

	_, err = Compile(`(?P<other>\d+)`, "")
	require.Error(t, err)
	assert.Contains(t, err.Error(), "names no timestamp group")
}

func TestCustomComponentGroups(t *testing.T) {
	f, err := Compile(`^(?P<day>\d\d)/(?P<month>\w+)/(?P<year>\d{4}) `+
		`(?P<hour>\d\d):(?P<min>\d\d):(?P<sec>\d\d)\.(?P<frac>\d+) (?P<zone>[+-]\d{4})`, "")
	require.NoError(t, err)
	assert.Equal(t, "custom", f.Name())

	at, ok := f.Parse("04/Mar/2026 05:06:07.250 +0200 GET /index.html")
	require.True(t, ok)
	assert.Equal(t, time.Date(2026, time.March, 4, 5, 6, 7, 250000000,
		time.FixedZone("", 2*60*60)).UTC(), at.UTC())

	_, ok = f.Parse("a line with no stamp at all")
	assert.False(t, ok, "a line the regex misses carries no time")
}

func TestCustomEpochGroups(t *testing.T) {
	f, err := Compile(`^(?P<epoch>\d+\.\d+) `, "")
	require.NoError(t, err)
	at, ok := f.Parse("1700000000.500 building")
	require.True(t, ok)
	assert.Equal(t, int64(1700000000), at.Unix())
	assert.Equal(t, 500000000, at.Nanosecond())

	f, err = Compile(`^(?P<epochms>\d{13})`, "")
	require.NoError(t, err)
	at, ok = f.Parse("1700000000500 building")
	require.True(t, ok)
	assert.Equal(t, int64(1700000000500), at.UnixMilli())

	f, err = Compile(`^(?P<epochns>\d{19})`, "")
	require.NoError(t, err)
	at, ok = f.Parse("1700000000500000000 building")
	require.True(t, ok)
	assert.Equal(t, int64(1700000000500000000), at.UnixNano())
}

func TestCustomTimeGroupUsesBuiltins(t *testing.T) {
	f, err := Compile(`^\[(?P<time>[^\]]+)\]`, "")
	require.NoError(t, err)
	at, ok := f.Parse("[2026-03-04 05:06:07] compiling foo.c")
	require.True(t, ok)
	assert.Equal(t, time.Date(2026, time.March, 4, 5, 6, 7, 0, time.UTC), at)
}

func TestCustomTimeGroupWithLayout(t *testing.T) {
	f, err := Compile(`^\((?P<time>[^)]+)\)`, "02.01.2006 15:04:05")
	require.NoError(t, err)
	at, ok := f.Parse("(04.03.2026 05:06:07) linking")
	require.True(t, ok)
	assert.Equal(t, time.Date(2026, time.March, 4, 5, 6, 7, 0, time.UTC), at)

	_, ok = f.Parse("(not a stamp) linking")
	assert.False(t, ok)
}

func TestLayoutWithoutRegexReadsLineStart(t *testing.T) {
	f, err := Compile("", "2006-01-02 15:04:05")
	require.NoError(t, err)
	assert.Equal(t, "layout", f.Name())
	at, ok := f.Parse("2026-03-04 05:06:07 starting build")
	require.True(t, ok)
	assert.Equal(t, time.Date(2026, time.March, 4, 5, 6, 7, 0, time.UTC), at)

	_, ok = f.Parse("starting build")
	assert.False(t, ok)
}

func TestClockOnlyCustomRollsOverMidnight(t *testing.T) {
	f, err := Compile(`^(?P<hour>\d\d):(?P<min>\d\d):(?P<sec>\d\d)`, "")
	require.NoError(t, err)
	late, ok := f.Parse("23:59:00 last step of the day")
	require.True(t, ok)
	early, ok := f.Parse("00:01:00 first step after it")
	require.True(t, ok)
	assert.True(t, early.After(late), "a wrapped clock moves forward, not back")
	assert.Equal(t, 2*time.Minute, early.Sub(late))
}

func TestCloneResetsRolloverState(t *testing.T) {
	f, err := Compile(`^(?P<hour>\d\d):(?P<min>\d\d):(?P<sec>\d\d)`, "")
	require.NoError(t, err)
	_, ok := f.Parse("23:59:00 late")
	require.True(t, ok)
	wrapped, ok := f.Parse("00:01:00 wrapped")
	require.True(t, ok)

	fresh, ok := f.Clone().Parse("00:01:00 wrapped")
	require.True(t, ok)
	assert.NotEqual(t, wrapped, fresh, "a clone starts its day again")
	assert.Nil(t, (*Format)(nil).Clone())
	assert.Equal(t, "none", (*Format)(nil).Name())
}

func TestMonthAndZoneForms(t *testing.T) {
	mo, ok := parseMonth("september")
	require.True(t, ok)
	assert.Equal(t, time.September, mo)
	mo, ok = parseMonth("11")
	require.True(t, ok)
	assert.Equal(t, time.November, mo)
	_, ok = parseMonth("Smarch")
	assert.False(t, ok)
	_, ok = parseMonth("13")
	assert.False(t, ok)

	assert.Equal(t, time.UTC, parseZoneText("Z"))
	assert.Equal(t, time.UTC, parseZoneText(""))
	_, off := time.Now().In(parseZoneText("-05:30")).Zone()
	assert.Equal(t, -(5*60+30)*60, off)
	_, off = time.Now().In(parseZoneText("+07")).Zone()
	assert.Equal(t, 7*60*60, off)
	assert.Equal(t, time.UTC, parseZoneText("+not-a-zone"))
}

func TestNamesAndGroupsAreCopies(t *testing.T) {
	names := Names()
	require.NotEmpty(t, names)
	names[0] = "clobbered"
	assert.NotEqual(t, "clobbered", Names()[0])

	groups := Groups()
	require.NotEmpty(t, groups)
	groups[0] = "clobbered"
	assert.NotEqual(t, "clobbered", Groups()[0])
}

func TestCustomRejectsOutOfRangeComponents(t *testing.T) {
	f, err := Compile(`^(?P<hour>\d\d):(?P<min>\d\d)`, "")
	require.NoError(t, err)
	_, ok := f.Parse("99:00 bogus")
	assert.False(t, ok)
}
