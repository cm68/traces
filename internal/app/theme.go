package app

import (
	"image/color"

	"fyne.io/fyne/v2"
	"fyne.io/fyne/v2/theme"
)

// PCBTracerTheme provides a custom theme for the application.
type PCBTracerTheme struct{}

var _ fyne.Theme = (*PCBTracerTheme)(nil)

func (t *PCBTracerTheme) Color(name fyne.ThemeColorName, variant fyne.ThemeVariant) color.Color {
	switch name {
	case theme.ColorNamePrimary:
		return color.NRGBA{R: 0x2E, G: 0x7D, B: 0x32, A: 0xFF} // Green for PCB
	case theme.ColorNameSelection:
		return color.NRGBA{R: 0xFF, G: 0xD5, B: 0x00, A: 0x80} // Gold for contacts
	case theme.ColorNameScrollBar:
		return color.NRGBA{R: 0x80, G: 0x80, B: 0x80, A: 0xFF} // Visible gray scrollbar
	default:
		return theme.DefaultTheme().Color(name, variant)
	}
}

func (t *PCBTracerTheme) Font(style fyne.TextStyle) fyne.Resource {
	return theme.DefaultTheme().Font(style)
}

func (t *PCBTracerTheme) Icon(name fyne.ThemeIconName) fyne.Resource {
	return theme.DefaultTheme().Icon(name)
}

func (t *PCBTracerTheme) Size(name fyne.ThemeSizeName) float32 {
	switch name {
	case theme.SizeNameScrollBar:
		return 16 // Wider scrollbar for easier grabbing
	case theme.SizeNameScrollBarSmall:
		return 12
	default:
		return theme.DefaultTheme().Size(name)
	}
}
