package cursor

import (
	"encoding/json"
	"strings"
)

var (
	smartDoubleQuotes = []rune{'«', '»', '\u201c', '\u201d', '\u275e', '\u201f', '\u201e', '\u275d'}
	smartSingleQuotes = []rune{'\u2018', '\u2019', '\u201a', '\u201b'}
)

func applyCursorToolArgFixes(toolName string, args map[string]any) map[string]any {
	args = normalizeCursorToolArgs(args)
	args = repairCursorStrReplaceArgs(toolName, args)
	return args
}

func normalizeCursorToolArgs(args map[string]any) map[string]any {
	if args == nil {
		return args
	}
	if _, ok := args["path"]; !ok {
		if filePath, ok := args["file_path"]; ok {
			args["path"] = filePath
			delete(args, "file_path")
		}
	}
	return args
}

func repairCursorStrReplaceArgs(toolName string, args map[string]any) map[string]any {
	if args == nil {
		return args
	}
	nameLower := strings.ToLower(strings.TrimSpace(toolName))
	if !strings.Contains(nameLower, "str_replace") && !strings.Contains(nameLower, "search_replace") {
		return args
	}

	oldValue, _ := args["old_string"].(string)
	if oldValue == "" {
		oldValue, _ = args["old_str"].(string)
	}
	if oldValue == "" {
		return args
	}

	normalizedOld := replaceCursorSmartQuotes(oldValue)
	if _, ok := args["old_string"]; ok {
		args["old_string"] = normalizedOld
	}
	if _, ok := args["old_str"]; ok {
		args["old_str"] = normalizedOld
	}
	if newValue, ok := args["new_string"].(string); ok {
		args["new_string"] = replaceCursorSmartQuotes(newValue)
	}
	if newValue, ok := args["new_str"].(string); ok {
		args["new_str"] = replaceCursorSmartQuotes(newValue)
	}
	return args
}

func replaceCursorSmartQuotes(text string) string {
	var builder strings.Builder
	for _, ch := range text {
		switch {
		case containsCursorRune(smartDoubleQuotes, ch):
			builder.WriteRune('"')
		case containsCursorRune(smartSingleQuotes, ch):
			builder.WriteRune('\'')
		default:
			builder.WriteRune(ch)
		}
	}
	return builder.String()
}

func containsCursorRune(values []rune, target rune) bool {
	for _, value := range values {
		if value == target {
			return true
		}
	}
	return false
}

func normalizeCursorFunctionArguments(functionData map[string]any) {
	if unwrapCursorApplyPatchFunctionArguments(functionData) {
		return
	}

	rawArgs := functionData["arguments"]
	if rawArgs == nil {
		return
	}

	argsStr, ok := rawArgs.(string)
	if !ok {
		return
	}

	var args map[string]any
	if err := json.Unmarshal([]byte(argsStr), &args); err != nil {
		return
	}

	args = applyCursorToolArgFixes(messagesStringValue(functionData["name"]), args)
	encoded, err := json.Marshal(args)
	if err != nil {
		return
	}
	functionData["arguments"] = string(encoded)
}
