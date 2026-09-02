package benchmarks

// Flow style takes different paths through the scanner, the parser and the
// printer than block style, so a regression in the flow paths hides behind
// healthy block numbers. Every benchmark here renders the same data in both
// styles at the same sizes, as adjacent sub-benchmarks.

import (
	"flag"
	"fmt"
	"strconv"
	"strings"
	"testing"

	"github.com/goccy/go-yaml"
	"github.com/goccy/go-yaml/lexer"
	"github.com/goccy/go-yaml/parser"
	"github.com/goccy/go-yaml/printer"
	"github.com/goccy/go-yaml/token"
)

var flowSizes = flag.String("flow.sizes", "1000,2000,4000,8000", "comma separated token counts for the flow vs block benchmarks")

// sink keeps printer output alive.
var sink string

// shape is one document layout with a renderer per style. n is a token budget
// rather than a record count: perUnit is what one unit of the shape costs in
// tokens, so nested/n=8000 and flat-map/n=8000 are comparable in size.
type shape struct {
	name    string
	perUnit int
	block   func(int) string
	flow    func(int) string
}

func (s shape) units(n int) int {
	if u := n / s.perUnit; u > 0 {
		return u
	}
	return 1
}

var shapes = []shape{
	{"flat-map", 1, flatMapBlock, flatMapFlow},
	{"flat-seq", 1, flatSeqBlock, flatSeqFlow},
	{"nested", 24, nestedBlock, nestedFlow},
}

func flatMapBlock(n int) string {
	var b strings.Builder
	for i := 0; i < n; i++ {
		fmt.Fprintf(&b, "key%d: value%d\n", i, i)
	}
	return b.String()
}

func flatMapFlow(n int) string {
	var b strings.Builder
	b.WriteByte('{')
	for i := 0; i < n; i++ {
		if i > 0 {
			b.WriteString(", ")
		}
		fmt.Fprintf(&b, "key%d: value%d", i, i)
	}
	b.WriteByte('}')
	return b.String()
}

func flatSeqBlock(n int) string {
	var b strings.Builder
	for i := 0; i < n; i++ {
		fmt.Fprintf(&b, "- value%d\n", i)
	}
	return b.String()
}

func flatSeqFlow(n int) string {
	var b strings.Builder
	b.WriteByte('[')
	for i := 0; i < n; i++ {
		if i > 0 {
			b.WriteString(", ")
		}
		fmt.Fprintf(&b, "value%d", i)
	}
	b.WriteByte(']')
	return b.String()
}

var flowRegions = []string{"us-east-1", "us-west-2", "eu-central-1", "ap-south-1"}

// nestedBlock is a sequence of mappings with a nested mapping and a nested
// sequence, so the two styles are compared at more than one indent level.
func nestedBlock(n int) string {
	var b strings.Builder
	b.WriteString("records:\n")
	for i := 0; i < n; i++ {
		fmt.Fprintf(&b, "  - id: %d\n", i+1)
		fmt.Fprintf(&b, "    name: record-%06d\n", i)
		fmt.Fprintf(&b, "    verified: %t\n", i%3 == 0)
		b.WriteString("    tags:\n")
		fmt.Fprintf(&b, "      - tag-%d\n", i%64)
		fmt.Fprintf(&b, "      - tag-%d\n", i%7)
		b.WriteString("    meta:\n")
		fmt.Fprintf(&b, "      region: %s\n", flowRegions[i%len(flowRegions)])
		fmt.Fprintf(&b, "      retries: %d\n", i%7)
	}
	return b.String()
}

func nestedFlow(n int) string {
	var b strings.Builder
	b.WriteString("{records: [")
	for i := 0; i < n; i++ {
		if i > 0 {
			b.WriteString(", ")
		}
		fmt.Fprintf(&b, "{id: %d, name: record-%06d, verified: %t, tags: [tag-%d, tag-%d], meta: {region: %s, retries: %d}}",
			i+1, i, i%3 == 0, i%64, i%7, flowRegions[i%len(flowRegions)], i%7)
	}
	b.WriteString("]}")
	return b.String()
}

func flowCounts(tb testing.TB) []int {
	tb.Helper()
	var out []int
	for _, field := range strings.Split(*flowSizes, ",") {
		field = strings.TrimSpace(field)
		if field == "" {
			continue
		}
		n, err := strconv.Atoi(field)
		if err != nil || n < 1 {
			tb.Fatalf("invalid -flow.sizes entry %q", field)
		}
		out = append(out, n)
	}
	if len(out) == 0 {
		tb.Fatal("-flow.sizes is empty")
	}
	return out
}

// eachStyle runs fn for every shape, style and size, named shape/style/n=N.
func eachStyle(b *testing.B, fn func(b *testing.B, src string)) {
	styles := []struct {
		name   string
		render func(shape, int) string
	}{
		{"block", func(s shape, n int) string { return s.block(s.units(n)) }},
		{"flow", func(s shape, n int) string { return s.flow(s.units(n)) }},
	}
	for _, sh := range shapes {
		for _, n := range flowCounts(b) {
			for _, st := range styles {
				src := st.render(sh, n)
				b.Run(fmt.Sprintf("%s/%s/n=%d", sh.name, st.name, n), func(b *testing.B) {
					fn(b, src)
				})
			}
		}
	}
}

// BenchmarkFlowStages splits the pipeline, so a regression can be attributed
// to a stage and to a style.
func BenchmarkFlowStages(b *testing.B) {
	stages := []struct {
		name string
		fn   func(string) error
	}{
		{"lexer.Tokenize", func(src string) error {
			lexer.Tokenize(src)
			return nil
		}},
		{"parser.ParseBytes", func(src string) error {
			_, err := parser.ParseBytes([]byte(src), 0)
			return err
		}},
		{"yaml.Unmarshal", func(src string) error {
			var v any
			return yaml.Unmarshal([]byte(src), &v)
		}},
	}
	eachStyle(b, func(b *testing.B, src string) {
		for _, s := range stages {
			b.Run(s.name, func(b *testing.B) {
				b.SetBytes(int64(len(src)))
				b.ReportAllocs()
				for i := 0; i < b.N; i++ {
					if err := s.fn(src); err != nil {
						b.Fatal(err)
					}
				}
			})
		}
	})
}

// BenchmarkFlowPrintTokens is where the two styles differ most: a flow
// document is a single line, so every token shares one line number. Watch
// ns/token, which should stay flat as n doubles.
func BenchmarkFlowPrintTokens(b *testing.B) {
	eachStyle(b, func(b *testing.B, src string) {
		tokens := lexer.Tokenize(src)
		b.SetBytes(int64(len(src)))
		b.ReportAllocs()
		b.ResetTimer()
		for i := 0; i < b.N; i++ {
			var p printer.Printer
			sink = p.PrintTokens(tokens)
		}
		b.StopTimer()
		reportPerToken(b, tokens)
	})
}

// BenchmarkFlowPrintErrorToken annotates a token in the middle of the
// document, the path every syntax error message takes. On a flow document the
// surrounding lines it collects are the whole document. Only one token is
// annotated, so ns/op is the metric here rather than ns/token.
func BenchmarkFlowPrintErrorToken(b *testing.B) {
	eachStyle(b, func(b *testing.B, src string) {
		tokens := lexer.Tokenize(src)
		if len(tokens) == 0 {
			b.Fatal("no tokens")
		}
		mid := tokens[len(tokens)/2]
		b.SetBytes(int64(len(src)))
		b.ReportAllocs()
		b.ResetTimer()
		for i := 0; i < b.N; i++ {
			var p printer.Printer
			sink = p.PrintErrorToken(mid, false)
		}
		b.StopTimer()
		b.ReportMetric(float64(len(tokens)), "tokens")
	})
}

// BenchmarkFlowEncode marshals the same value in both styles.
func BenchmarkFlowEncode(b *testing.B) {
	var v any
	if err := yaml.Unmarshal([]byte(nestedBlock(64)), &v); err != nil {
		b.Fatal(err)
	}
	styles := []struct {
		name string
		opts []yaml.EncodeOption
	}{
		{"block", nil},
		{"flow", []yaml.EncodeOption{yaml.Flow(true)}},
	}
	for _, st := range styles {
		out, err := yaml.MarshalWithOptions(v, st.opts...)
		if err != nil {
			b.Fatal(err)
		}
		b.Run("nested/"+st.name, func(b *testing.B) {
			b.SetBytes(int64(len(out)))
			b.ReportAllocs()
			for i := 0; i < b.N; i++ {
				if _, err := yaml.MarshalWithOptions(v, st.opts...); err != nil {
					b.Fatal(err)
				}
			}
		})
	}
}

// reportPerToken normalises by token count, which is what makes a block row
// and a flow row of the same n comparable.
func reportPerToken(b *testing.B, tokens token.Tokens) {
	b.ReportMetric(float64(len(tokens)), "tokens")
	b.ReportMetric(float64(b.Elapsed().Nanoseconds())/float64(b.N)/float64(len(tokens)), "ns/token")
}
