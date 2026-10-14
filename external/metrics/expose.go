//go:build metrics

package metrics

import (
	"bytes"
	"fmt"
	"strings"

	vm "github.com/VictoriaMetrics/metrics"
)

// The text format escapes a label value and a help text each with a set of
// its own: a quote only matters inside a label value.
var (
	labelEscaper = strings.NewReplacer(`\`, `\\`, `"`, `\"`, "\n", `\n`)
	helpEscaper  = strings.NewReplacer(`\`, `\\`, "\n", `\n`)
)

// series is the name of one sample, name{k="v",...} for the pairs in kv, with
// every value escaped. The library takes it as it is, so a name it would
// reject (a raw line feed) can never get that far.
func series(name string, kv ...string) string {
	if len(kv) < 2 {
		return name
	}
	var b strings.Builder
	b.WriteString(name)
	b.WriteByte('{')
	for i := 0; i+1 < len(kv); i += 2 {
		if i > 0 {
			b.WriteByte(',')
		}
		b.WriteString(kv[i])
		b.WriteString(`="`)
		b.WriteString(labelEscaper.Replace(kv[i+1]))
		b.WriteByte('"')
	}
	b.WriteByte('}')
	return b.String()
}

// family is one metric family and what is printed in front of its samples,
// once. An empty typ prints no TYPE line: the family stays untyped.
type family struct{ name, typ, help string }

func (f family) writeHeader(b *bytes.Buffer) {
	fmt.Fprintf(b, "# HELP %s %s\n", f.name, helpEscaper.Replace(f.help))
	if f.typ != "" {
		fmt.Fprintf(b, "# TYPE %s %s\n", f.name, f.typ)
	}
}

// writeRows writes the header and then rows, or nothing when rows is empty: a
// family with no sample is absent.
func (f family) writeRows(b, rows *bytes.Buffer) {
	if rows.Len() == 0 {
		return
	}
	f.writeHeader(b)
	b.Write(rows.Bytes())
}

// instruments is a family whose samples the library keeps, in a set of its
// own. The library writes no help or type but an empty "# HELP name", and a
// set holding one family is what lets the header go in front of exactly its
// samples. None of the sets is shared with the library's package-level one, so
// nothing else in the process writes into a scrape.
type instruments struct {
	family
	set *vm.Set
}

func newInstruments(f family) *instruments {
	return &instruments{family: f, set: vm.NewSet()}
}

// counter is the counter of the series name{kv...}, created at zero.
func (i *instruments) counter(kv ...string) *vm.Counter {
	return i.set.GetOrCreateCounter(series(i.name, kv...))
}

// histogram is the histogram of the series name{kv...} with real le buckets
// at bounds, created empty.
func (i *instruments) histogram(bounds []float64, kv ...string) *vm.PrometheusHistogram {
	return i.set.GetOrCreatePrometheusHistogramExt(series(i.name, kv...), bounds)
}

func (i *instruments) writeTo(b *bytes.Buffer) {
	var rows bytes.Buffer
	i.set.WritePrometheus(&rows)
	i.writeRows(b, &rows)
}

// writeLibrary writes the Go runtime and process figures the library reads on
// every call. The open and the allowed file descriptors are a call of their
// own in the library; they belong to the process figures all the same.
func writeLibrary(b *bytes.Buffer) {
	var raw bytes.Buffer
	vm.WriteProcessMetrics(&raw)
	vm.WriteFDMetrics(&raw)
	annotate(b, raw.String())
}

// annotate copies the library's exposition, with one HELP line in front of
// the first sample of each name. There is no TYPE line on purpose: the
// library's own types (a counter named go_cgo_calls_count, a gauge named
// process_io_read_bytes_total) would trip a linter that checks names against
// types, and an untyped family is read as a gauge.
func annotate(b *bytes.Buffer, text string) {
	seen := map[string]bool{}
	for line := range strings.Lines(text) {
		line = strings.TrimSuffix(line, "\n")
		if line == "" {
			continue
		}
		if !strings.HasPrefix(line, "#") {
			name := line
			if n := strings.IndexAny(line, "{ "); n >= 0 {
				name = line[:n]
			}
			if !seen[name] {
				seen[name] = true
				family{name: name, help: libraryHelp(name)}.writeHeader(b)
			}
		}
		b.WriteString(line)
		b.WriteByte('\n')
	}
}

func libraryHelp(name string) string {
	switch {
	case strings.HasPrefix(name, "go_"):
		return "Go runtime metric written by github.com/VictoriaMetrics/metrics."
	case strings.HasPrefix(name, "process_"):
		return "Process metric written by github.com/VictoriaMetrics/metrics."
	}
	return "Metric written by github.com/VictoriaMetrics/metrics."
}
