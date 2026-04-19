package augment

import (
	"encoding/json"
	"fmt"
	"strings"

	"github.com/Wei-Shaw/sub2api/internal/pkg/apicompat"
)

type ToolMetadata struct {
	MCPServerName string
	MCPToolName   string
}

type PreparedOpenAIResponsesRequest struct {
	Body                []byte
	Stream              bool
	ToolMetaByName      map[string]ToolMetadata
	SupportToolUseStart bool
}

type responsesUserSegment struct {
	kind      string
	text      string
	data      string
	mediaType string
}

type responsesToolCall struct {
	CallID    string
	Name      string
	Arguments string
}

func PrepareChatStreamOpenAIResponsesRequest(raw []byte) (*PreparedOpenAIResponsesRequest, error) {
	_, req, err := prepareChatStreamRequest(raw)
	if err != nil {
		return nil, err
	}

	body, toolMetaByName, supportToolUseStart, err := buildOpenAIResponsesRequestBody(req)
	if err != nil {
		return nil, err
	}

	return &PreparedOpenAIResponsesRequest{
		Body:                body,
		Stream:              req.IsStreaming(),
		ToolMetaByName:      toolMetaByName,
		SupportToolUseStart: supportToolUseStart,
	}, nil
}

func buildOpenAIResponsesRequestBody(req *Request) ([]byte, map[string]ToolMetadata, bool, error) {
	if req == nil {
		return nil, nil, false, fmt.Errorf("augment request is nil")
	}

	instructions := buildOpenAIResponsesInstructions(req)
	input, err := buildOpenAIResponsesInput(req)
	if err != nil {
		return nil, nil, false, err
	}

	inputRaw, err := json.Marshal(input)
	if err != nil {
		return nil, nil, false, err
	}

	maxTokens := effectiveMaxTokens(req.MaxTokens)
	store := false
	responsesReq := apicompat.ResponsesRequest{
		Model:           strings.TrimSpace(req.Model),
		Input:           inputRaw,
		Instructions:    instructions,
		MaxOutputTokens: &maxTokens,
		Stream:          req.IsStreaming(),
		Include:         []string{"reasoning.encrypted_content"},
		Store:           &store,
		Reasoning: &apicompat.ResponsesReasoning{
			Effort:  "high",
			Summary: "auto",
		},
	}

	if tools := convertOpenAIResponsesTools(req.EffectiveTools()); len(tools) > 0 && !req.Silent {
		responsesReq.Tools = tools
		toolChoice, err := json.Marshal("auto")
		if err != nil {
			return nil, nil, false, err
		}
		responsesReq.ToolChoice = toolChoice
	}

	body, err := json.Marshal(responsesReq)
	if err != nil {
		return nil, nil, false, err
	}

	return body, buildToolMetaByName(req.EffectiveTools()), resolveSupportToolUseStart(req), nil
}

func buildOpenAIResponsesInstructions(req *Request) string {
	system := buildSystem(req)
	if len(system) == 0 {
		return ""
	}
	parts := make([]string, 0, len(system))
	for _, block := range system {
		if block.Type == "text" {
			if text := strings.TrimSpace(block.Text); text != "" {
				parts = append(parts, text)
			}
		}
	}
	return strings.TrimSpace(strings.Join(parts, "\n\n"))
}

func buildOpenAIResponsesInput(req *Request) ([]map[string]any, error) {
	history, currentNodes := preprocessHistoryForAPI(req)
	input := make([]map[string]any, 0)

	for idx, entry := range history {
		requestNodes := entry.EffectiveRequestNodes()
		if userContent := buildOpenAIResponsesMessageContent(entry.RequestMessage, requestNodes, nil, false); userContent != nil {
			input = append(input, map[string]any{
				"type":    "message",
				"role":    "user",
				"content": userContent,
			})
		}

		responseNodes := entry.EffectiveResponseNodes()
		assistantText := strings.TrimSpace(entry.ResponseText)
		if assistantText == "" {
			assistantText = extractAssistantTextFromResponseNodes(responseNodes)
		}
		if assistantText != "" {
			input = append(input, map[string]any{
				"type":    "message",
				"role":    "assistant",
				"content": assistantText,
			})
		}

		for _, toolCall := range extractToolCallsFromResponseNodes(responseNodes) {
			input = append(input, map[string]any{
				"type":      "function_call",
				"call_id":   toolCall.CallID,
				"name":      toolCall.Name,
				"arguments": toolCall.Arguments,
			})
		}

		if idx+1 < len(history) {
			input = append(input, buildFunctionCallOutputsFromNodes(history[idx+1].EffectiveRequestNodes())...)
		}
	}

	input = append(input, buildFunctionCallOutputsFromNodes(currentNodes)...)
	if userContent := buildOpenAIResponsesMessageContent(req.Message, currentNodes, req.EffectiveContext(), true, req.Images...); userContent != nil {
		input = append(input, map[string]any{
			"type":    "message",
			"role":    "user",
			"content": userContent,
		})
	}

	return repairOpenAIResponsesToolCallPairs(input), nil
}

func buildOpenAIResponsesMessageContent(message string, nodes []Node, ctx *ContextBlock, includeContextExtras bool, topImages ...string) any {
	segments := buildOpenAIResponsesUserSegments(message, nodes, topImages...)
	if includeContextExtras && !hasNonToolUserSegments(segments) {
		if contextText := buildContextText(ctx); contextText != "" {
			segments = append(segments, responsesUserSegment{kind: "text", text: contextText})
		}
	}
	return buildOpenAIResponsesContentFromSegments(segments)
}

func buildOpenAIResponsesUserSegments(message string, nodes []Node, topImages ...string) []responsesUserSegment {
	segments := make([]responsesUserSegment, 0)
	appendText := func(text string) {
		text = strings.TrimSpace(text)
		if text == "" {
			return
		}
		if len(segments) > 0 {
			last := segments[len(segments)-1]
			if last.kind == "text" && strings.TrimSpace(last.text) == text {
				return
			}
		}
		segments = append(segments, responsesUserSegment{kind: "text", text: text})
	}

	appendText(message)
	for _, node := range nodes {
		if node.Type == 1 {
			continue
		}
		if node.Type == 2 {
			if block := imageBlockForNode(node); block != nil && block.Source != nil && strings.TrimSpace(block.Source.Data) != "" {
				segments = append(segments, responsesUserSegment{
					kind:      "image",
					data:      strings.TrimSpace(block.Source.Data),
					mediaType: strings.TrimSpace(block.Source.MediaType),
				})
				continue
			}
		}
		if text := strings.TrimSpace(formatPromptNode(node)); text != "" {
			appendText(text)
		}
	}
	for _, raw := range topImages {
		if block := buildRawImageBlock(raw, defaultImageMediaType); block != nil && block.Source != nil && strings.TrimSpace(block.Source.Data) != "" {
			segments = append(segments, responsesUserSegment{
				kind:      "image",
				data:      strings.TrimSpace(block.Source.Data),
				mediaType: strings.TrimSpace(block.Source.MediaType),
			})
		}
	}
	return segments
}

func hasNonToolUserSegments(segments []responsesUserSegment) bool {
	for _, segment := range segments {
		switch segment.kind {
		case "text":
			if strings.TrimSpace(segment.text) != "" {
				return true
			}
		case "image":
			if strings.TrimSpace(segment.data) != "" {
				return true
			}
		}
	}
	return false
}

func buildOpenAIResponsesContentFromSegments(segments []responsesUserSegment) any {
	if len(segments) == 0 {
		return nil
	}

	hasImage := false
	for _, segment := range segments {
		if segment.kind == "image" && strings.TrimSpace(segment.data) != "" {
			hasImage = true
			break
		}
	}
	if !hasImage {
		parts := make([]string, 0, len(segments))
		for _, segment := range segments {
			if segment.kind == "text" {
				if text := strings.TrimSpace(segment.text); text != "" {
					parts = append(parts, text)
				}
			}
		}
		if len(parts) == 0 {
			return nil
		}
		return strings.Join(parts, "\n\n")
	}

	content := make([]map[string]any, 0, len(segments))
	textBuf := make([]string, 0)
	flushText := func() {
		if len(textBuf) == 0 {
			return
		}
		text := strings.TrimSpace(strings.Join(textBuf, "\n\n"))
		textBuf = textBuf[:0]
		if text == "" {
			return
		}
		content = append(content, map[string]any{
			"type": "input_text",
			"text": text,
		})
	}
	for _, segment := range segments {
		switch segment.kind {
		case "text":
			if text := strings.TrimSpace(segment.text); text != "" {
				textBuf = append(textBuf, text)
			}
		case "image":
			if strings.TrimSpace(segment.data) == "" {
				continue
			}
			flushText()
			mediaType := strings.TrimSpace(segment.mediaType)
			if mediaType == "" {
				mediaType = defaultImageMediaType
			}
			content = append(content, map[string]any{
				"type":      "input_image",
				"image_url": fmt.Sprintf("data:%s;base64,%s", mediaType, strings.TrimSpace(segment.data)),
				"detail":    "auto",
			})
		}
	}
	flushText()
	if len(content) == 0 {
		return nil
	}
	return content
}

func buildFunctionCallOutputsFromNodes(nodes []Node) []map[string]any {
	outputs := make([]map[string]any, 0)
	for _, node := range nodes {
		if node.Type != 1 || node.ToolResultNode == nil {
			continue
		}
		toolUseID := strings.TrimSpace(effectiveToolUseID(node.ToolResultNode))
		if toolUseID == "" {
			continue
		}
		outputs = append(outputs, map[string]any{
			"type":    "function_call_output",
			"call_id": toolUseID,
			"output":  toolResultContentText(buildToolResultContent(node.ToolResultNode)),
		})
	}
	return outputs
}

func extractAssistantTextFromResponseNodes(nodes []Node) string {
	parts := make([]string, 0)
	for _, node := range nodes {
		switch node.Type {
		case 0, 2:
			if text := strings.TrimSpace(nodeText(node.TextNode)); text != "" {
				parts = append(parts, text)
			}
		}
	}
	return strings.TrimSpace(strings.Join(parts, "\n\n"))
}

func extractToolCallsFromResponseNodes(nodes []Node) []responsesToolCall {
	seen := make(map[string]struct{})
	toolCalls := make([]responsesToolCall, 0)
	for _, node := range nodes {
		if node.ToolUse == nil || (node.Type != 5 && node.Type != 7) {
			continue
		}
		callID := strings.TrimSpace(node.ToolUse.ToolUseID)
		if callID == "" {
			continue
		}
		if _, ok := seen[callID]; ok {
			continue
		}
		name := strings.TrimSpace(node.ToolUse.ToolName)
		if name == "" {
			continue
		}
		seen[callID] = struct{}{}
		arguments := strings.TrimSpace(node.ToolUse.InputJSON)
		if arguments == "" {
			arguments = "{}"
		}
		toolCalls = append(toolCalls, responsesToolCall{CallID: callID, Name: name, Arguments: arguments})
	}
	return toolCalls
}

func buildToolMetaByName(defs []ToolDefinition) map[string]ToolMetadata {
	if len(defs) == 0 {
		return nil
	}
	out := make(map[string]ToolMetadata)
	for _, def := range defs {
		name := strings.TrimSpace(def.Name)
		if name == "" {
			continue
		}
		meta := ToolMetadata{MCPServerName: strings.TrimSpace(def.McpServerName), MCPToolName: strings.TrimSpace(def.McpToolName)}
		if meta.MCPServerName == "" && meta.MCPToolName == "" {
			continue
		}
		out[name] = meta
	}
	return out
}

func resolveSupportToolUseStart(req *Request) bool {
	if req == nil || req.FeatureDetectionFlags == nil {
		return false
	}
	if value, ok := req.FeatureDetectionFlags["support_tool_use_start"].(bool); ok && value {
		return true
	}
	if value, ok := req.FeatureDetectionFlags["supportToolUseStart"].(bool); ok && value {
		return true
	}
	return false
}

func convertOpenAIResponsesTools(defs []ToolDefinition) []apicompat.ResponsesTool {
	if len(defs) == 0 {
		return nil
	}
	tools := make([]apicompat.ResponsesTool, 0, len(defs))
	for _, def := range defs {
		name := strings.TrimSpace(def.Name)
		if name == "" {
			continue
		}
		schema := coerceOpenAIStrictJSONSchema(def.EffectiveInputSchema())
		parameters, err := json.Marshal(schema)
		if err != nil {
			continue
		}
		strict := true
		tool := apicompat.ResponsesTool{Type: "function", Name: name, Parameters: parameters, Strict: &strict}
		if description := strings.TrimSpace(def.Description); description != "" {
			tool.Description = description
		}
		tools = append(tools, tool)
	}
	return tools
}

func coerceOpenAIStrictJSONSchema(value any) any {
	switch typed := value.(type) {
	case []any:
		out := make([]any, 0, len(typed))
		for _, item := range typed {
			out = append(out, coerceOpenAIStrictJSONSchema(item))
		}
		return out
	case map[string]any:
		out := make(map[string]any, len(typed))
		for key, item := range typed {
			out[key] = coerceOpenAIStrictJSONSchema(item)
		}
		hasObjectType := false
		switch typeValue := out["type"].(type) {
		case string:
			hasObjectType = strings.EqualFold(strings.TrimSpace(typeValue), "object")
		case []any:
			for _, item := range typeValue {
				if text, ok := item.(string); ok && strings.EqualFold(strings.TrimSpace(text), "object") {
					hasObjectType = true
					break
				}
			}
		}
		properties, hasProps := out["properties"].(map[string]any)
		if hasObjectType || hasProps {
			if !hasObjectType {
				out["type"] = "object"
			}
			if !hasProps {
				properties = map[string]any{}
				out["properties"] = properties
			}
			out["additionalProperties"] = false
			required := make([]string, 0, len(properties))
			for key := range properties {
				required = append(required, key)
			}
			out["required"] = required
		}
		if out["additionalProperties"] != nil && out["additionalProperties"] != false {
			out["additionalProperties"] = false
		}
		return out
	default:
		return value
	}
}

func repairOpenAIResponsesToolCallPairs(items []map[string]any) []map[string]any {
	if len(items) == 0 {
		return items
	}

	type pendingToolCall struct {
		CallID    string
		Name      string
		Arguments string
	}

	out := make([]map[string]any, 0, len(items)+2)
	pending := make(map[string]pendingToolCall)
	bufferedOrphans := make([]map[string]any, 0)

	injectMissing := func() {
		if len(pending) == 0 {
			return
		}
		for _, toolCall := range pending {
			out = append(out, map[string]any{
				"type":    "function_call_output",
				"call_id": toolCall.CallID,
				"output":  fmt.Sprintf("[tool_result not available: call_id=%s tool_name=%s]", toolCall.CallID, toolCall.Name),
			})
		}
		pending = make(map[string]pendingToolCall)
	}

	flushBufferedOrphans := func() {
		if len(bufferedOrphans) == 0 {
			return
		}
		for _, item := range bufferedOrphans {
			callID := strings.TrimSpace(stringValue(item["call_id"]))
			output := strings.TrimSpace(stringValue(item["output"]))
			if output == "" {
				output = "(empty tool result)"
			}
			out = append(out, map[string]any{
				"type":    "message",
				"role":    "user",
				"content": fmt.Sprintf("[orphan_function_call_output call_id=%s]\n%s", callID, output),
			})
		}
		bufferedOrphans = bufferedOrphans[:0]
	}

	closePendingToolPhase := func() {
		injectMissing()
		flushBufferedOrphans()
	}

	for _, item := range items {
		itemType := strings.TrimSpace(strings.ToLower(stringValue(item["type"])))
		if itemType == "" {
			itemType = "message"
		}

		if len(pending) > 0 {
			switch itemType {
			case "function_call":
				out = append(out, item)
				callID := strings.TrimSpace(stringValue(item["call_id"]))
				name := strings.TrimSpace(stringValue(item["name"]))
				if callID != "" && name != "" {
					pending[callID] = pendingToolCall{
						CallID:    callID,
						Name:      name,
						Arguments: strings.TrimSpace(stringValue(item["arguments"])),
					}
				}
				continue
			case "function_call_output":
				callID := strings.TrimSpace(stringValue(item["call_id"]))
				if callID != "" {
					if _, ok := pending[callID]; ok {
						delete(pending, callID)
						out = append(out, item)
						if len(pending) == 0 {
							flushBufferedOrphans()
						}
						continue
					}
				}
				bufferedOrphans = append(bufferedOrphans, item)
				continue
			default:
				closePendingToolPhase()
			}
		}

		switch itemType {
		case "function_call":
			out = append(out, item)
			callID := strings.TrimSpace(stringValue(item["call_id"]))
			name := strings.TrimSpace(stringValue(item["name"]))
			if callID != "" && name != "" {
				pending[callID] = pendingToolCall{
					CallID:    callID,
					Name:      name,
					Arguments: strings.TrimSpace(stringValue(item["arguments"])),
				}
			}
		case "function_call_output":
			callID := strings.TrimSpace(stringValue(item["call_id"]))
			output := strings.TrimSpace(stringValue(item["output"]))
			if output == "" {
				output = "(empty tool result)"
			}
			out = append(out, map[string]any{
				"type":    "message",
				"role":    "user",
				"content": fmt.Sprintf("[orphan_function_call_output call_id=%s]\n%s", callID, output),
			})
		default:
			out = append(out, item)
		}
	}

	closePendingToolPhase()
	return out
}
