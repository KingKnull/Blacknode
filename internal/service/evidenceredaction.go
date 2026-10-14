package service

import "regexp"

var evidenceRedactors = []*regexp.Regexp{
	regexp.MustCompile(`(?s)-----BEGIN [A-Z ]*PRIVATE KEY-----.*?-----END [A-Z ]*PRIVATE KEY-----`),
	regexp.MustCompile(`(?im)(?:authorization\s*[:=]\s*(?:bearer|basic)\s+)[^\s"']+`),
	regexp.MustCompile(`(?im)(?:[A-Za-z0-9_]*(?:password|passwd|secret|api[_-]?key|access[_-]?token|auth[_-]?token)[A-Za-z0-9_]*["']?\s*[:=]\s*)["']?[^\s,;"'}]+["']?`),
	regexp.MustCompile(`\b(?:AKIA|ASIA)[A-Z0-9]{16}\b`),
	regexp.MustCompile(`\b(?:ghp_|github_pat_|sk-ant-)[A-Za-z0-9_-]{16,}\b`),
	regexp.MustCompile(`\beyJ[A-Za-z0-9_-]+\.[A-Za-z0-9_-]+\.[A-Za-z0-9_-]+\b`),
	regexp.MustCompile(`(?i)://[^\s/:]+:[^\s/@]+@`),
}

func redactEvidence(text string) string {
	for _, re := range evidenceRedactors {
		text = re.ReplaceAllString(text, "[REDACTED]")
	}
	return text
}

func (s *PlatformService) RedactEvidence(text string) string { return redactEvidence(text) }
