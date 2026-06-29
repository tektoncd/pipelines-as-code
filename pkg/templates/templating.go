package templates

import (
	"encoding/json"
	"net/http"
	"reflect"
	"strings"

	"github.com/google/cel-go/common/types"
	"github.com/google/cel-go/common/types/traits"
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
// "body", "headers", or "files". If it does and both `rawEvent` and `headers`
// are not nil, it attempts to retrieve the value for the key using the
// `cel.Value` function and returns the corresponding string
// representation. If the key does not have any of the mentioned prefixes, the
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
//   - changedFiles (map[string]interface{}): A map of changed files that may be
//     used to retrieve values for placeholders with keys that have a prefix of
//     "files".
func ReplacePlaceHoldersVariables(template string, dico map[string]string, rawEvent any, headers http.Header, changedFiles map[string]interface{}) string {
	return keys.ParamsRe.ReplaceAllStringFunc(template, func(s string) string {
		parts := keys.ParamsRe.FindStringSubmatch(s)
		key := strings.TrimSpace(parts[1])
		if strings.HasPrefix(key, "body") || strings.HasPrefix(key, "headers") || strings.HasPrefix(key, "files") {
			if rawEvent != nil && headers != nil {
				// convert headers to map[string]string
				headerMap := make(map[string]string)
				for k, v := range headers {
					headerMap[k] = v[0]
				}
				val, err := cel.Value(key, rawEvent, headerMap, map[string]string{}, changedFiles)
				if err != nil {
					return s
				}
				var raw interface{}
				var b []byte

				switch val.(type) {
				case types.String:
					if v, ok := val.Value().(string); ok {
						b = []byte(v)
					}
				case types.Bytes:
					raw, err = val.ConvertToNative(structType)
					if err == nil {
						if sv, ok := raw.(*structpb.Value); ok {
							b, err = sv.MarshalJSON()
							if err != nil {
								b = []byte{}
							}
						}
					}
				case types.Double, types.Int:
					raw, err = val.ConvertToNative(structType)
					if err == nil {
						if sv, ok := raw.(*structpb.Value); ok {
							b, err = sv.MarshalJSON()
							if err != nil {
								b = []byte{}
							}
						}
					}
				case traits.Lister:
					raw, err = val.ConvertToNative(listType)
					if err == nil {
						if pm, ok := raw.(proto.Message); ok {
							s, err := protojson.Marshal(pm)
							if err == nil {
								b = s
							}
						}
					}
				case traits.Mapper:
					raw, err = val.ConvertToNative(mapType)
					if err == nil {
						if pm, ok := raw.(proto.Message); ok {
							s, err := protojson.Marshal(pm)
							if err == nil {
								b = s
							}
						}
					}
				case types.Bool:
					raw, err = val.ConvertToNative(structType)
					if err == nil {
						if sv, ok := raw.(*structpb.Value); ok {
							b, err = json.Marshal(sv.GetBoolValue())
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
