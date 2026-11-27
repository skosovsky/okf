package bundle

import (
	"fmt"
	"math/big"
	"strconv"
	"strings"

	"gopkg.in/yaml.v3"
)

// YAMLScalarForV03Migration returns a source token and its decoded value
// when a legacy scalar needs different syntax to keep the same value in Core.
// Only a timestamp in an OKF temporal field may use Core's implicit string
// type because the field parses the same timestamp in either representation.
func YAMLScalarForV03Migration(node *yaml.Node, temporalField bool) (token, value string, err error) {
	raw := node.Value
	switch node.Tag {
	case "!!str":
		if node.Style == 0 && YAMLCoreStringNeedsQuotes(raw) {
			return strconv.Quote(raw), raw, nil
		}
	case "!!int":
		var legacy big.Int
		if _, ok := legacy.SetString(raw, 0); !ok {
			return "", "", fmt.Errorf("cannot preserve legacy integer %q", raw)
		}
		tag, core := yamlCoreKeyScalar(node)
		decimal := legacy.String()
		if tag != "!!int" || core != decimal {
			return decimal, decimal, nil
		}
	case "!!float":
		if yamlCoreScalarTag(node) != "!!float" {
			converted := strings.ReplaceAll(raw, "_", "")
			if yamlCoreDecimal.MatchString(converted) {
				converted += ".0"
			}
			if !yamlCoreFloat.MatchString(converted) {
				return "", "", fmt.Errorf("cannot preserve legacy float %q", raw)
			}
			return converted, converted, nil
		}
	case "!!timestamp":
		if node.Style == 0 && (!temporalField || node.Anchor != "") {
			return "!!timestamp " + strconv.Quote(raw), raw, nil
		}
	}
	return "", raw, nil
}
