// Package capturetest builds typed capture requests for driver tests.
package capturetest

import (
	"github.com/davecheney/logic"
	"github.com/davecheney/logic/internal/capture"
)

// Request returns a request for the digital inputs of a that c selects.
func Request(a logic.Analyzer, c capture.Config) logic.CaptureRequest {
	req := logic.CaptureRequest{
		Rate: c.Rate, Samples: c.Samples, Falling: c.Falling, AnyEdge: c.AnyEdge,
		PreTrigger: c.PreTrigger, Auto: c.Auto,
	}
	caps := a.Capabilities()
	for r := range caps.Resources() {
		if !r.Kind.Has(logic.KindDigitalIn) {
			continue
		}
		index := r.ID.Index()
		if c.Channels&(1<<index) != 0 {
			req.Inputs = append(req.Inputs, r.ID)
		}
		if int(index) == c.Source {
			req.Trigger = r.ID
		}
	}
	return req
}
