package perf

import (
	"bytes"
	"fmt"
	"strings"
	"testing"

	"github.com/samber/lo"
	sdktrace "go.opentelemetry.io/otel/sdk/trace"
	"go.opentelemetry.io/otel/sdk/trace/tracetest"
	"go.opentelemetry.io/otel/trace"
	"go.viam.com/test"
)

func TestOtelDevelopmentExporter(t *testing.T) {
	setup := func() (*OtelDevelopmentExporter, *bytes.Buffer) {
		e := NewOtelDevelopmentExporter()
		buff := &bytes.Buffer{}
		e.outputWriter = buff
		e.Start()
		return e, buff
	}

	t.Run("no spans", func(t *testing.T) {
		t.SkipNow()
		e, buff := setup()
		e.ExportSpans(t.Context(), nil)
		e.Stop()
		output := buff.String()
		test.That(t, output, test.ShouldBeEmpty)
	})

	t.Run("single span", func(t *testing.T) {
		t.SkipNow()
		e, buff := setup()
		spanStup := &tracetest.SpanStub{
			Name: "single",
		}
		spans := []sdktrace.ReadOnlySpan{
			spanStup.Snapshot(),
		}
		e.ExportSpans(t.Context(), spans)
		e.Stop()
		output := buff.String()
		test.That(t, output, test.ShouldNotBeEmpty)
		lines := lo.Filter(strings.Split(output, "\n"), filterEmpty)
		test.That(t, lines, test.ShouldHaveLength, 1)
		test.That(t, lines[0], test.ShouldContainSubstring, "single:")
		test.That(t, lines[0], test.ShouldContainSubstring, fmt.Sprintf("Calls: %5d", 1))
	})
	t.Run("multiple root spans", func(t *testing.T) {
		t.SkipNow()
		e, buff := setup()
		spanStub := &tracetest.SpanStub{
			Name: "repeated",
		}
		spans := []sdktrace.ReadOnlySpan{
			spanStub.Snapshot(),
			spanStub.Snapshot(),
			spanStub.Snapshot(),
		}
		e.ExportSpans(t.Context(), spans)
		e.Stop()
		output := buff.String()
		test.That(t, output, test.ShouldNotBeEmpty)
		lines := lo.Filter(strings.Split(output, "\n"), filterEmpty)
		test.That(t, lines, test.ShouldHaveLength, 3)
		for _, line := range lines {
			test.That(t, line, test.ShouldContainSubstring, "repeated:")
			test.That(t, line, test.ShouldContainSubstring, callsStr(1))
		}
	})
	t.Run("nested spans", func(t *testing.T) {
		e, buff := setup()
		rootSpan := &tracetest.SpanStub{
			Name: "root",
			SpanContext: trace.NewSpanContext(trace.SpanContextConfig{
				SpanID: trace.SpanID{0xDE, 0xAD, 0xBE, 0xEF},
			}),
		}
		childCount := 3

		rootSpanRO := rootSpan.Snapshot()
		childSpan := &tracetest.SpanStub{
			Name:           "child",
			ChildSpanCount: childCount,
		}

		// Create a child span with a unique id for each "call"
		spans := []sdktrace.ReadOnlySpan{}
		for i := range childCount {
			childSpan.Parent = rootSpanRO.SpanContext()
			childSpan.SpanContext = trace.NewSpanContext(trace.SpanContextConfig{
				TraceID: rootSpanRO.SpanContext().TraceID(),
				SpanID:  trace.SpanID{1 << i},
			})
			spans = append(spans, childSpan.Snapshot())
		}
		// Append the root span to the end of the batch. The development exporter
		// is currently dependent on child spans being sorted before their parents,
		// which so far matches what we see with real tracers and trace providers.
		spans = append(spans, rootSpanRO)

		e.ExportSpans(t.Context(), spans)
		e.Stop()
		output := buff.String()
		test.That(t, output, test.ShouldNotBeEmpty)
		lines := lo.Filter(strings.Split(output, "\n"), filterEmpty)
		test.That(t, lines, test.ShouldHaveLength, 2)
		test.That(t, lines[0], test.ShouldContainSubstring, "root:")
		test.That(t, lines[0], test.ShouldContainSubstring, callsStr(1))
		test.That(t, lines[1], test.ShouldContainSubstring, "child:")
		test.That(t, lines[1], test.ShouldContainSubstring, callsStr(childCount))
		// Just make sure the child spans are indented and the root isn't, don't
		// get as specific as matching the exact amount of whitespace used to
		// indent.
		test.That(t, lines[0], test.ShouldNotStartWith, "  ")
		test.That(t, lines[1], test.ShouldStartWith, "  ")
	})
}

func callsStr(count int) string {
	return fmt.Sprintf("Calls: %5d", count)
}

func filterEmpty(line string, _ int) bool {
	return strings.TrimSpace(line) != ""
}
