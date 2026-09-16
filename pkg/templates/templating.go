package templates

import (
	"encoding/json"
	"net/http"
	"reflect"
	"strings"

	"cel.dev/cel-go/common/types"
	"cel.dev/cel-go/common/types/traits"
	"github.com/openshift-pipelines/pipelines-as-code/pkg/apis/pipelinesascode/keys"
	"github.com/openshift-pipelines/pipelines-as-code/pkg/cel"
	"google.golang.org/protobuf/encoding/protojson"
	"google.golang.org/protobuf/proto"
	"google.golang.org/protobuf/types/known/structpb"
)

var (
	structType = reflect.TypeOf(&structpb.Value{})
	listType   = reflect.TypeOf(&structpb.ListValue{})
	mapType    = reflect.TypeOf(&structpb.Struct{})
)

// ReplacePlaceHoldersVariables is a function that replaces placeholders in a
// given string template with their corresponding values. The placeholders are
// expected to be in the format `{{key}}`, where `key` is the identifier for a
// value.
//
// The function first checks if the key in the placeholder has a prefix of
// "body", "headers", "files", or "cel:". If it does and both `rawEvent` and `headers`
// are not nil, it attempts to retrieve the value for the key using the
// `cel.Value` function and returns the corresponding string
// representation. The "cel:" prefix allows evaluating arbitrary CEL expressions
// with access to body, headers, files, and pac (standard PAC parameters) namespaces.
// If the key does not have any of the mentioned prefixes, the
// function checks if the key exists in the `dico` map. If it does, the
// function replaces the placeholder with the corresponding value from the
// `dico` map.
//
// Parameters:
//   - template (string): The input string that may contain placeholders in the
//     format `{{key}}`.
//   - dico (map[string]string): A dictionary mapping keys to their corresponding
//     string values. If a placeholder's key is found in this dictionary, it will
//     be replaced with the corresponding value.
//   - rawEvent (any): The raw event data that may be used to retrieve values for
//     placeholders with keys that have a prefix of "body", "headers", or "files".
//   - headers (http.Header): The HTTP headers that may be used to retrieve
//     values for placeholders with keys that have a prefix of "headers".
//   - changedFiles (map[string]any): A map of changed files that may be
//     used to retrieve values for placeholders with keys that have a prefix of
//     "files".
func ReplacePlaceHoldersVariables(template string, dico map[string]string, rawEvent any, headers http.Header, changedFiles map[string]any) string {
	return keys.ParamsRe.ReplaceAllStringFunc(template, func(s string) string {
		parts := keys.ParamsRe.FindStringSubmatch(s)
		key := strings.TrimSpace(parts[1])

		// Check for cel: prefix first - it allows arbitrary CEL expressions
		isCelExpr := strings.HasPrefix(key, "cel:")

		if strings.HasPrefix(key, "body") || strings.HasPrefix(key, "headers") || strings.HasPrefix(key, "files") || isCelExpr {
			// Check specific requirements for each prefix
			canEvaluate := false
			celExpr := key
			switch {
			case isCelExpr:
				canEvaluate = true
				celExpr = strings.TrimSpace(strings.TrimPrefix(key, "cel:"))
			case strings.HasPrefix(key, "body") && rawEvent != nil:
				canEvaluate = true
			case strings.HasPrefix(key, "headers") && headers != nil:
				canEvaluate = true
			case strings.HasPrefix(key, "files"):
				canEvaluate = true // files evaluation doesn't depend on rawEvent or headers
			}

			if canEvaluate {
				// convert headers to map[string]string
				headerMap := make(map[string]string)
				for k, v := range headers {
					headerMap[k] = v[0]
				}
				// For cel: prefix, pass dico as pacParams so pac.* variables are available
				pacParams := map[string]string{}
				if isCelExpr {
					pacParams = dico
				}
				val, err := cel.Value(celExpr, rawEvent, headerMap, pacParams, changedFiles)
				if err != nil {
					// For cel: prefix, return empty string on error
					if isCelExpr {
						return ""
					}
					return s
				}
				var raw any
				var b []byte

				switch val.(type) {
				case types.String:
					if v, ok := val.Value().(string); ok {
						b = []byte(v)
					}

				case types.Bytes, types.Double, types.Int:
					raw, err = val.ConvertToNative(structType)
					if err == nil {
						if structVal, ok := raw.(*structpb.Value); ok {
							b, err = structVal.MarshalJSON()
							if err != nil {
								b = []byte{}
							}
						}
					}

				case traits.Lister:
					raw, err = val.ConvertToNative(listType)
					if err == nil {
						if msg, ok := raw.(proto.Message); ok {
							b, err = protojson.Marshal(msg)
							if err != nil {
								b = []byte{}
							}
						}
					}

				case traits.Mapper:
					raw, err = val.ConvertToNative(mapType)
					if err == nil {
						if msg, ok := raw.(proto.Message); ok {
							b, err = protojson.Marshal(msg)
							if err != nil {
								b = []byte{}
							}
						}
					}

				case types.Bool:
					raw, err = val.ConvertToNative(structType)
					if err == nil {
						if structVal, ok := raw.(*structpb.Value); ok {
							b, err = json.Marshal(structVal.GetBoolValue())
							if err != nil {
								b = []byte{}
							}
						}
					}

				default:
					raw, err = val.ConvertToNative(reflect.TypeOf([]byte{}))
					if err == nil {
						if v, ok := raw.([]byte); ok {
							b = v
						}
					}
				}
				return string(b)
			}
			return s
		}
		if _, ok := dico[key]; !ok {
			return s
		}
		return dico[key]
	})
}
