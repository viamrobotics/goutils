// Package perf exposes application performance utilities.
package perf

import (
	"encoding/hex"
	"fmt"
	"log"
	"regexp"
	"sync"

	"go.opencensus.io/trace"
)

type mySpanInfo struct {
	toPrint string
	id      string
}

type niceLoggingSpanExporter struct {
	mu       sync.Mutex
	children map[string][]mySpanInfo
}

// NewNiceLoggingSpanExporter creates a new Exporter that prints to the default log.
func NewNiceLoggingSpanExporter() trace.Exporter {
	return &niceLoggingSpanExporter{children: map[string][]mySpanInfo{}}
}

var reZero = regexp.MustCompile(`^0+$`)

func (e *niceLoggingSpanExporter) printTree(root string, padding string) {
	for _, s := range e.children[root] {
		log.Printf("%s %s\n", padding, s.toPrint)
		e.printTree(s.id, padding+"  ")
	}
	delete(e.children, root)
}

func (e *niceLoggingSpanExporter) ExportSpan(s *trace.SpanData) {
	e.mu.Lock()
	defer e.mu.Unlock()

	length := (s.EndTime.UnixNano() - s.StartTime.UnixNano()) / (1000 * 1000)
	myinfo := fmt.Sprintf("%s %d ms", s.Name, length)

	if s.Annotations != nil {
		for _, a := range s.Annotations {
			myinfo = myinfo + " " + a.Message
		}
	}

	spanID := hex.EncodeToString(s.SpanID[:])
	parentSpanID := hex.EncodeToString(s.ParentSpanID[:])

	if !reZero.MatchString(parentSpanID) {
		e.children[parentSpanID] = append(e.children[parentSpanID], mySpanInfo{myinfo, spanID})
		return
	}

	// i'm the top of the tree, go me
	log.Println(myinfo)
	e.printTree(hex.EncodeToString(s.SpanContext.SpanID[:]), "  ")
}
