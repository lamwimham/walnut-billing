package service

import (
	"crypto/sha256"
	"encoding/hex"
	"regexp"
	"strings"
)

type AdminEmailProjection struct {
	Masked      string `json:"masked"`
	Fingerprint string `json:"fingerprint"`
	Domain      string `json:"domain"`
}

type AdminActorProjection struct {
	Type        string `json:"type"`
	ID          string `json:"id,omitempty"`
	Masked      string `json:"masked,omitempty"`
	Fingerprint string `json:"fingerprint,omitempty"`
	Domain      string `json:"domain,omitempty"`
}

type AdminPrivacyProjector struct{}

func NewAdminPrivacyProjector() AdminPrivacyProjector {
	return AdminPrivacyProjector{}
}

func (AdminPrivacyProjector) ProjectEmail(email string) AdminEmailProjection {
	return AdminEmailProjection{
		Masked:      maskEmail(email),
		Fingerprint: emailFingerprint(email),
		Domain:      emailDomain(email),
	}
}

func (p AdminPrivacyProjector) ProjectActor(actor string) AdminActorProjection {
	actor = strings.TrimSpace(actor)
	if actor == "" {
		return AdminActorProjection{Type: "unknown"}
	}
	if looksLikeEmail(actor) {
		email := p.ProjectEmail(actor)
		return AdminActorProjection{Type: "email", Masked: email.Masked, Fingerprint: email.Fingerprint, Domain: email.Domain}
	}
	if strings.HasPrefix(actor, "usr_") {
		return AdminActorProjection{Type: "user", ID: actor}
	}
	if isKnownOperatorActor(actor) {
		return AdminActorProjection{Type: "operator", ID: actor}
	}
	return AdminActorProjection{Type: "opaque", Masked: maskToken(actor), Fingerprint: tokenFingerprint(actor)}
}

func (p AdminPrivacyProjector) RedactFreeText(value string) string {
	if strings.TrimSpace(value) == "" {
		return value
	}
	return emailPattern.ReplaceAllStringFunc(value, func(raw string) string {
		email := p.ProjectEmail(raw)
		if email.Fingerprint == "" {
			return email.Masked
		}
		return email.Masked + "#" + email.Fingerprint
	})
}

func (p AdminPrivacyProjector) RedactIdentifier(value string) string {
	value = strings.TrimSpace(value)
	if value == "" {
		return ""
	}
	if looksLikeEmail(value) {
		email := p.ProjectEmail(value)
		if email.Fingerprint == "" {
			return email.Masked
		}
		return email.Masked + "#" + email.Fingerprint
	}
	return value
}

var emailPattern = regexp.MustCompile(`(?i)[a-z0-9._%+\-]+@[a-z0-9.\-]+\.[a-z]{2,}`)

func looksLikeEmail(value string) bool {
	value = strings.TrimSpace(value)
	return value != "" && emailPattern.MatchString(value) && emailPattern.FindString(value) == value
}

func isKnownOperatorActor(actor string) bool {
	switch actor {
	case "admin", "system", "unknown", "mock", "creem", "wechat", "alipay", "billing_provider":
		return true
	default:
		return strings.HasPrefix(actor, "legacy-admin-")
	}
}

func tokenFingerprint(value string) string {
	value = strings.TrimSpace(value)
	if value == "" {
		return ""
	}
	digest := sha256.Sum256([]byte("walnut-admin-token-v1:" + value))
	return hex.EncodeToString(digest[:])[:12]
}
