package main

import "github.com/gechr/primer/input"

func prlEditorStyles() input.EditorStyles {
	return input.EditorStyles{
		BlurredText: styleSubtle,
		Counter:     styleSubtle,
		Dirty:       styleDirty.Bold(true),
		DimLabel:    styleSubtle,
		FocusedText: styleOK,
		Header:      styleHeading.Bold(true),
		HelpKey:     styleHelpKeyDim,
		HelpText:    styleSubtle,
		Label:       styleLabel.Bold(true),
	}
}
