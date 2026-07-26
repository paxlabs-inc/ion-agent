package controlplane

import (
	"encoding/json"
)

// StreamFrame is the browser WebSocket server-to-client envelope.
type StreamFrame struct {
	Type     string         `json:"type"`
	Recovery *Recovery      `json:"recovery,omitempty"`
	Event    *Event         `json:"event,omitempty"`
	Error    *ProtocolError `json:"error,omitempty"`
}

type clientStreamFrame struct {
	Type     string `json:"type"`
	ClientID string `json:"client_id"`
	Sequence uint64 `json:"sequence"`
}

func decodeClientStreamFrame(payload []byte) (clientStreamFrame, error) {
	var frame clientStreamFrame
	if err := json.Unmarshal(payload, &frame); err != nil {
		return clientStreamFrame{}, err
	}
	return frame, nil
}
