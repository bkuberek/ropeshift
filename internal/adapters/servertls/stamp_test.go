package servertls

import (
	"testing"
	"time"
)

// A modification time is an instant, and two spellings of one instant are one time: a comparison that read the
// location or the clock reading in it would read a file as changed when nothing wrote to it.
func TestStampsOfOneInstantAreTheSameWhateverTheirLocation(t *testing.T) {
	t.Parallel()
	instant := time.Unix(1_900_000_000, 0)
	spellings := []time.Time{instant.UTC(), instant.In(time.FixedZone("east", 3600)), instant.In(time.FixedZone("west", -7200)), instant.Local()}
	for _, one := range spellings {
		for _, other := range spellings {
			a := stamp{cert: fileStamp{one, 10}, key: fileStamp{one, 20}}
			b := stamp{cert: fileStamp{other, 10}, key: fileStamp{other, 20}}
			if !a.same(b) {
				t.Errorf("%v and %v are one instant", one, other)
			}
		}
	}
	base := stamp{cert: fileStamp{instant, 10}, key: fileStamp{instant, 20}}
	for name, changed := range map[string]stamp{
		"a later certificate":  {cert: fileStamp{instant.Add(time.Nanosecond), 10}, key: base.key},
		"a later key":          {cert: base.cert, key: fileStamp{instant.Add(time.Second), 20}},
		"a larger certificate": {cert: fileStamp{instant, 11}, key: base.key},
		"a larger key":         {cert: base.cert, key: fileStamp{instant, 21}},
	} {
		if base.same(changed) {
			t.Errorf("%s reads as unchanged", name)
		}
	}
}
