package cursor

import (
	"encoding/json"
	"os"
	"regexp"
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

	filePath, _ := args["path"].(string)
	if filePath == "" {
		filePath, _ = args["file_path"].(string)
	}
	if filePath == "" {
		return args
	}

	content, err := os.ReadFile(filePath)
	if err != nil {
		return args
	}
	contentStr := string(content)
	if strings.Contains(contentStr, oldValue) {
		return args
	}

	normalizedOld := replaceCursorSmartQuotes(oldValue)
	if normalizedOld != oldValue && strings.Contains(contentStr, normalizedOld) {
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

	pattern, err := regexp.Compile(buildCursorFuzzyPattern(oldValue))
	if err != nil {
		return args
	}
	matches := pattern.FindAllString(contentStr, -1)
	if len(matches) != 1 {
		return args
	}

	if _, ok := args["old_string"]; ok {
		args["old_string"] = matches[0]
	}
	if _, ok := args["old_str"]; ok {
		args["old_str"] = matches[0]
	}
	if newValue, ok := args["new_string"].(string); ok {
		args["new_string"] = replaceCursorSmartQuotes(newValue)
	}
	if newValue, ok := args["new_str"].(string); ok {
		args["new_str"] = replaceCursorSmartQuotes(newValue)
	}
	return args
}

func buildCursorFuzzyPattern(text string) string {
	var builder strings.Builder
	for _, ch := range text {
		switch {
		case containsCursorRune(smartDoubleQuotes, ch) || ch == '"':
			builder.WriteString(`["\u00ab\u201c\u201d\u275e\u201f\u201e\u275d\u00bb]`)
		case containsCursorRune(smartSingleQuotes, ch) || ch == '\'':
			builder.WriteString(`['\u2018\u2019\u201a\u201b]`)
		case ch == ' ' || ch == '\t':
			builder.WriteString(`\s+`)
		case ch == '\\':
			builder.WriteString(`\\{1,2}`)
		default:
			builder.WriteString(regexp.QuoteMeta(string(ch)))
		}
	}
	return builder.String()
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

func unwrapCursorApplyPatchFunctionArguments(functionData map[string]any) {
	if strings.TrimSpace(messagesStringValue(functionData["name"])) != "ApplyPatch" {
		return
	}
	argsStr, ok := functionData["arguments"].(string)
	if !ok || argsStr == "" {
		return
	}
	functionData["arguments"] = unwrapCursorApplyPatchArgumentsString(argsStr)
}

func unwrapCursorApplyPatchArgumentsString(arguments string) string {
	var payload map[string]any
	if err := json.Unmarshal([]byte(arguments), &payload); err != nil {
		return arguments
	}
	patch, ok := payload["patch"].(string)
	if !ok {
		return arguments
	}
	return patch
}
