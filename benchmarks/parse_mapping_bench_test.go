package benchmarks

import (
	"fmt"
	"runtime"
	"strings"
	"testing"

	"github.com/goccy/go-yaml/parser"
)

// wideMappingSrc renders n sibling entries as one mapping, in block style or
// in flow style, so the two can be compared on identical data.
func wideMappingSrc(n int, flow bool) []byte {
	var b strings.Builder
	b.Grow(n * 20)
	if flow {
		b.WriteByte('{')
	}
	for i := 0; i < n; i++ {
		if flow {
			if i > 0 {
				b.WriteString(", ")
			}
			fmt.Fprintf(&b, "key%d: value%d", i, i)
			continue
		}
		fmt.Fprintf(&b, "key%d: value%d\n", i, i)
	}
	if flow {
		b.WriteByte('}')
	}
	return []byte(b.String())
}

// BenchmarkParseMapping complements BenchmarkParseScaling by measuring the
// same mapping in both styles, and by reporting the two derived metrics that
// say whether the cost per entry is constant:
//
//	gc/op    collector pressure, which the tail copies used to dominate
//	ns/key   wall clock per entry, flat when parsing is linear
//
// Flow style is measured alongside because it was already parsed iteratively
// and is the control: it should not move.
func BenchmarkParseMapping(b *testing.B) {
	for _, style := range []struct {
		name string
		flow bool
	}{
		{"block", false},
		{"flow", true},
	} {
		for _, keys := range []int{1000, 4000, 16000} {
			src := wideMappingSrc(keys, style.flow)
			b.Run(fmt.Sprintf("%s/keys=%d", style.name, keys), func(b *testing.B) {
				b.SetBytes(int64(len(src)))
				b.ReportAllocs()

				var before, after runtime.MemStats
				runtime.GC()
				runtime.ReadMemStats(&before)

				b.ResetTimer()
				for i := 0; i < b.N; i++ {
					if _, err := parser.ParseBytes(src, 0); err != nil {
						b.Fatal(err)
					}
				}
				b.StopTimer()

				runtime.ReadMemStats(&after)
				b.ReportMetric(float64(after.NumGC-before.NumGC)/float64(b.N), "gc/op")
				b.ReportMetric(float64(b.Elapsed().Nanoseconds())/float64(b.N)/float64(keys), "ns/key")
			})
		}
	}
}
