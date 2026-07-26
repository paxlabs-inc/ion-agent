package adapters

import (
	"encoding/json"
	"fmt"
	"io"
	"strings"

	"github.com/paxlabs-inc/ion-agent/internal/controlplane"
)

func decode(payload json.RawMessage, target any) error {
	decoder := json.NewDecoder(strings.NewReader(string(payload)))
	decoder.DisallowUnknownFields()
	if err := decoder.Decode(target); err != nil {
		return controlplane.PublicError{
			Code: controlplane.ErrorInvalid, Message: "invalid operation payload",
		}
	}
	var trailing json.RawMessage
	if err := decoder.Decode(&trailing); err != io.EOF {
		return fmt.Errorf("controlplane adapters: payload contains multiple JSON values")
	}
	return nil
}
