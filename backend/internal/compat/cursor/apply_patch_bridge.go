package cursor

import (
	"encoding/json"
	"strings"
)

const (
	cursorApplyPatchToolName = "ApplyPatch"
	cursorApplyPatchArgName  = "patch"
)

const cursorApplyPatchArgDescription = `Complete raw ApplyPatch patch text. Put the entire patch in this string, starting with *** Begin Patch and ending with *** End Patch.

Examples:
Add a file:
*** Begin Patch
*** Add File: path/to/file.txt
+new line
*** End Patch

Update or delete lines:
*** Begin Patch
*** Update File: path/to/file.txt
@@
-old line
+new line
*** End Patch

Delete a file:
*** Begin Patch
*** Delete File: path/to/file.txt
*** End Patch`

func cursorApplyPatchFunctionParameters() map[string]any {
	return map[string]any{
		"type": "object",
		"properties": map[string]any{
			cursorApplyPatchArgName: map[string]any{
				"type":        "string",
				"description": cursorApplyPatchArgDescription,
			},
		},
		"required": []any{cursorApplyPatchArgName},
	}
}

func encodeCursorApplyPatchFunctionArguments(patch string) string {
	encoded, err := json.Marshal(map[string]string{cursorApplyPatchArgName: patch})
	if err != nil {
		return `{"patch":""}`
	}
	return string(encoded)
}

func decodeCursorApplyPatchFunctionArguments(arguments string) (string, bool) {
	var payload map[string]any
	if err := json.Unmarshal([]byte(arguments), &payload); err != nil {
		return "", false
	}
	patch, ok := payload[cursorApplyPatchArgName].(string)
	if !ok {
		return "", false
	}
	return patch, true
}

func hasCursorApplyPatchFunctionWrapper(arguments string) bool {
	_, ok := decodeCursorApplyPatchFunctionArguments(arguments)
	return ok
}

func unwrapCursorApplyPatchFunctionArguments(functionData map[string]any) bool {
	if strings.TrimSpace(messagesStringValue(functionData["name"])) != cursorApplyPatchToolName {
		return false
	}
	patch, ok := decodeCursorApplyPatchFunctionArguments(messagesStringValue(functionData["arguments"]))
	if !ok {
		return false
	}
	functionData["arguments"] = patch
	return true
}
