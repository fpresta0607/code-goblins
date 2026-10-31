package project

import (
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"strings"

	"github.com/fpresta0607/code-goblins/internal/routing"
)

type DeliveryPolicy struct {
	Posture string `json:"posture"`
}

func (policy DeliveryPolicy) Validate() error {
	switch policy.Posture {
	case "", "no-mistakes", "no-mistakes-prod-only", "direct-PR", "local-only":
		return nil
	default:
		return fmt.Errorf("project manifest: unknown delivery posture %q", policy.Posture)
	}
}

type DeliverySelection struct {
	Mode         string       `json:"mode"`
	Posture      string       `json:"posture"`
	Surface      string       `json:"surface"`
	Risk         string       `json:"risk"`
	Reason       string       `json:"reason"`
	Verification Verification `json:"verification"`
	RequiresCI   bool         `json:"requires_ci"`
}

// SelectDelivery resolves intake once. Surface and risk describe the actual
// change, never the directory it happens to edit. Only the CFO's explicit
// exception may narrow the selected rigor.
func SelectDelivery(manifest Manifest, brief, requested, exception string) (DeliverySelection, error) {
	selection := DeliverySelection{
		Mode: "no-mistakes", Posture: manifest.Delivery.Posture,
		Surface: "unknown", Risk: "unknown", Reason: "missing or uncertain delivery configuration",
		Verification: manifest.Verification, RequiresCI: true,
	}
	fields := map[string]string{}
	isDelivery := false
	isMalformed := false
	for _, line := range strings.Split(brief, "\n") {
		line = strings.TrimSpace(line)
		if strings.HasPrefix(line, "## ") {
			if line == "## Delivery" && fields["section"] != "" {
				isMalformed = true
			}
			isDelivery = line == "## Delivery"
			if isDelivery {
				fields["section"] = "seen"
			}
			continue
		}
		if !isDelivery {
			continue
		}
		key, value, hasColon := strings.Cut(line, ":")
		if !hasColon || (key != "surface" && key != "risk" && key != "reason" && key != "mode") {
			continue
		}
		if _, exists := fields[key]; exists {
			isMalformed = true
		}
		fields[key] = strings.TrimSpace(value)
	}
	if value := fields["surface"]; value != "" {
		selection.Surface = value
	}
	if value := fields["risk"]; value != "" {
		selection.Risk = value
	}
	if assessment := routing.Classify(brief); assessment.Risk == "high" {
		selection.Risk = string(assessment.Class)
	}
	hasChecks := len(selection.Verification.Fast) > 0
	for _, command := range selection.Verification.Fast {
		if len(command) == 0 || strings.TrimSpace(command[0]) == "" || validateCommand(command) != nil {
			hasChecks = false
		}
	}
	isKnownSurface := selection.Surface == "internal" || selection.Surface == "product"
	switch {
	case manifest.Delivery.Validate() != nil || manifest.Delivery.Posture == "":
	case isMalformed:
		selection.Reason = "malformed or repeated delivery declaration"
	case !isKnownSurface:
		selection.Reason = "mixed or unknown change surface requires independent validation"
	case selection.Risk != "low":
		selection.Reason = "high or unknown change risk requires independent validation: " + selection.Risk
	case strings.TrimSpace(fields["reason"]) == "" || strings.ContainsAny(fields["reason"], "{}"):
		selection.Reason = "no concrete change assessment was recorded"
	case !hasChecks:
		selection.Reason = "simple delivery requires defined project checks"
	default:
		selection.Reason = fields["reason"]
		switch manifest.Delivery.Posture {
		case "direct-PR", "local-only":
			selection.Mode = manifest.Delivery.Posture
		case "no-mistakes-prod-only":
			if selection.Surface == "internal" {
				selection.Mode = "direct-PR"
			}
		}
	}
	if requested == "" {
		requested = fields["mode"]
	}
	if requested != "" && requested != selection.Mode {
		if requested == "no-mistakes" {
			selection.Mode, selection.Reason = requested, "explicit strict delivery: "+selection.Reason
		} else if strings.TrimSpace(exception) != "" && !strings.ContainsAny(exception, "\r\n") && hasChecks && (requested == "direct-PR" || requested == "local-only") {
			selection.Mode, selection.Reason = requested, "explicit CFO-authorized exception: "+exception
		} else {
			return DeliverySelection{}, fmt.Errorf("delivery: requested %s conflicts with selected %s: %s", requested, selection.Mode, selection.Reason)
		}
	}
	selection.RequiresCI = selection.Mode != "local-only"
	return selection, nil
}

func (selection DeliverySelection) Encode() (string, string, error) {
	data, err := json.Marshal(selection)
	if err != nil {
		return "", "", err
	}
	sum := sha256.Sum256(data)
	return string(data), hex.EncodeToString(sum[:]), nil
}

func ReadDeliverySelection(data []byte, hash, mode string) (DeliverySelection, error) {
	sum := sha256.Sum256(data)
	if hex.EncodeToString(sum[:]) != hash {
		return DeliverySelection{}, errors.New("delivery: frozen selection changed")
	}
	var selection DeliverySelection
	if err := json.Unmarshal(data, &selection); err != nil {
		return DeliverySelection{}, err
	}
	if selection.Mode != mode {
		return DeliverySelection{}, errors.New("delivery: task mode differs from its frozen selection")
	}
	return selection, nil
}
