package service

import (
	"context"
	"time"

	coderws "github.com/coder/websocket"
)

// OpenAIWSDownstreamConn is the client side of a Responses connection. A named
// stream uses a logical connection so the existing forwarding, admission and
// billing lifecycle never shares mutable state with another stream.
type OpenAIWSDownstreamConn interface {
	Read(context.Context) (coderws.MessageType, []byte, error)
	Write(context.Context, coderws.MessageType, []byte) error
	Close(coderws.StatusCode, string) error
	CloseNow() error
}

var _ OpenAIWSDownstreamConn = (*coderws.Conn)(nil)

func openAIWSDownstreamFrameArrival(conn OpenAIWSDownstreamConn, fallback time.Time) time.Time {
	if source, ok := conn.(interface{ LastFrameArrival() time.Time }); ok {
		if arrival := source.LastFrameArrival(); !arrival.IsZero() {
			return arrival
		}
	}
	return fallback
}

// CompleteOpenAIWSDownstreamTurn runs after admission slots, policy state and
// usage have been handed off. Upstream failover is still the same logical turn.
func CompleteOpenAIWSDownstreamTurn(conn OpenAIWSDownstreamConn, turnErr error) {
	if turnErr != nil {
		return
	}
	if notifier, ok := conn.(interface{ TurnComplete() }); ok {
		notifier.TurnComplete()
	}
}
