package agent

import (
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"strings"
)

func LoadCorpus(r io.Reader) ([]Case, string, error) {
	data, err := io.ReadAll(io.LimitReader(r, 4<<20+1))
	if err != nil {
		return nil, "", err
	}
	if len(data) > 4<<20 {
		return nil, "", errors.New("corpus exceeds 4 MiB")
	}
	var cases []Case
	dec := json.NewDecoder(strings.NewReader(string(data)))
	dec.DisallowUnknownFields()
	if err := dec.Decode(&cases); err != nil {
		return nil, "", err
	}
	if err := dec.Decode(new(any)); err != io.EOF {
		return nil, "", errors.New("trailing corpus content")
	}
	if err := ValidateCases(cases); err != nil {
		return nil, "", err
	}
	h := sha256.Sum256(data)
	return cases, hex.EncodeToString(h[:]), nil
}

func ValidateCases(cases []Case) error {
	if len(cases) == 0 {
		return errors.New("empty corpus")
	}
	seen := map[string]bool{}
	for _, c := range cases {
		if c.ID == "" || seen[c.ID] {
			return fmt.Errorf("empty or duplicate case id %q", c.ID)
		}
		seen[c.ID] = true
		if c.Tier != "realistic" && c.Tier != "mechanism_only" {
			return fmt.Errorf("%s: invalid tier", c.ID)
		}
		if c.Category == "" || c.Question == "" || c.Expected.Answer == "" || len(c.Expected.Evidence) == 0 || len(c.Expected.Support) == 0 {
			return fmt.Errorf("%s: incomplete case", c.ID)
		}
		for _, arm := range [][]Artifact{c.Control, c.Treatment} {
			if len(arm) == 0 {
				return fmt.Errorf("%s: empty arm", c.ID)
			}
			ids := map[string]bool{}
			for _, a := range arm {
				if a.ID == "" || a.Path == "" || a.Content == "" || ids[a.ID] {
					return fmt.Errorf("%s: invalid artifact", c.ID)
				}
				ids[a.ID] = true
			}
		}
		for _, id := range c.Expected.Evidence {
			foundControl, foundTreatment := false, false
			for _, a := range c.Control {
				if a.ID == id {
					foundControl = true
				}
			}
			for _, a := range c.Treatment {
				if a.ID == id {
					foundTreatment = true
				}
			}
			if !foundControl || !foundTreatment {
				return fmt.Errorf("%s: evidence %q unavailable to both arms", c.ID, id)
			}
		}
		for _, support := range c.Expected.Support {
			if support.ArtifactID == "" || (support.Quote == "" && (support.ControlQuote == "" || support.TreatmentQuote == "")) {
				return fmt.Errorf("%s: empty support quote", c.ID)
			}
			isEvidence := false
			for _, id := range c.Expected.Evidence {
				if id == support.ArtifactID {
					isEvidence = true
				}
			}
			if !isEvidence {
				return fmt.Errorf("%s: support quote does not match expected evidence", c.ID)
			}
			for armIndex, arm := range [][]Artifact{c.Control, c.Treatment} {
				quote := support.Quote
				if armIndex == 0 && support.ControlQuote != "" {
					quote = support.ControlQuote
				}
				if armIndex == 1 && support.TreatmentQuote != "" {
					quote = support.TreatmentQuote
				}
				found := false
				for _, a := range arm {
					if a.ID == support.ArtifactID && strings.Contains(a.Content, quote) {
						found = true
					}
				}
				if !found {
					return fmt.Errorf("%s: support quote %q absent from arm %d", c.ID, quote, armIndex)
				}
			}
		}
	}
	return nil
}

func normalize(s string) string {
	s = strings.Trim(strings.ToLower(strings.TrimSpace(s)), "\"'`.,;:!? ")
	return strings.Join(strings.Fields(s), " ")
}

// Grade deliberately uses exact normalized answers and an evidence ID.
// Semantic synonyms must be preregistered as aliases; an LLM grader cannot
// silently alter the threshold after seeing results.
func Grade(c Case, obs Observation, failure string) string {
	if failure != "" {
		return OperationalFailure
	}
	if obs.Refused {
		return Refusal
	}
	a := normalize(obs.Answer)
	if a == "" {
		return Ungradable
	}
	for _, stale := range c.Expected.StaleAnswers {
		if a == normalize(stale) {
			return Stale
		}
	}
	correct := a == normalize(c.Expected.Answer)
	for _, alias := range c.Expected.Aliases {
		correct = correct || a == normalize(alias)
	}
	if !correct {
		return Wrong
	}
	evidence := map[string]bool{}
	for _, e := range obs.Evidence {
		evidence[e] = true
	}
	for _, e := range c.Expected.Evidence {
		if evidence[e] {
			return Correct
		}
	}
	return Ungradable
}
