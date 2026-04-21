package tui

import (
	"fmt"
	"strings"
)

// RenderRight renders the activity stream panel.
// filter, if non-empty, restricts display to rows from that instance.
// events[0] is oldest; only the last height-3 rows are shown.
func RenderRight(events []EventRow, filter string, width, height int) string {
	var sb strings.Builder

	filterLabel := "[all agents]"
	if filter != "" {
		filterLabel = "[" + filter + "]"
	}
	sb.WriteString(styleSection.Render(fmt.Sprintf("Activity stream %s", filterLabel)) + "\n")
	sb.WriteString(strings.Repeat("─", width-1) + "\n")

	var visible []EventRow
	for _, e := range events {
		if filter == "" || e.Instance == filter {
			visible = append(visible, e)
		}
	}

	maxRows := height - 3
	if maxRows < 1 {
		maxRows = 1
	}
	if len(visible) > maxRows {
		visible = visible[len(visible)-maxRows:]
	}

	for _, e := range visible {
		sb.WriteString(renderEventRow(e, width) + "\n")
	}
	return sb.String()
}

func renderEventRow(e EventRow, width int) string {
	evTime := styleEvTime.Render(e.Time.Format("15:04:05"))
	evInst := styleEvInstance.Copy().Foreground(instanceColor(e.Instance)).Render(e.Instance)

	var evType, evContent string
	switch e.Kind {
	case KindTool:
		evType = styleEvTypeTool.Render(e.ToolName)
		evContent = styleContent.Render(e.Content)
	case KindText:
		evType = styleEvTypeText.Render("text")
		evContent = styleContent.Render(e.Content)
	case KindResult:
		evType = styleEvTypeResult.Render("result")
		evContent = styleEvTypeResult.Render(e.Content)
	}

	used := 8 + 10 + 8
	if remaining := width - used - 2; remaining > 0 {
		if e.Kind == KindResult {
			evContent = styleEvTypeResult.MaxWidth(remaining).Render(e.Content)
		} else {
			evContent = styleContent.MaxWidth(remaining).Render(e.Content)
		}
	}

	return evTime + evInst + evType + evContent
}
