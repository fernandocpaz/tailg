package tui

import (
    "strings"
    "time"

    tea "github.com/charmbracelet/bubbletea"
    "github.com/charmbracelet/lipgloss"
)

const (
    startupIntroFrameCount   = 18
    startupIntroInterval     = 70 * time.Millisecond
    startupIntroSpriteWidth  = 64
    startupIntroSpriteHeight = 36 // Two vertical pixels per terminal cell.
    startupIntroMinWidth     = startupIntroSpriteWidth
    startupIntroMinHeight    = startupIntroSpriteHeight / 2
)

type startupIntroTick struct{ frame int }

func startupIntroCmd(frame int) tea.Cmd {
    return tea.Tick(startupIntroInterval, func(time.Time) tea.Msg {
        return startupIntroTick{frame: frame}
    })
}

// Hand-drawn pixel sprite: sweeping tail, blue body and wing, long neck,
// crested head, visible eye, and hooked golden beak. Each letter is a color.
// Keeping the silhouette independent of the leg frames makes the sprint fluid.
var startupRoadRunnerPixels = []string{
    "",
    "                                 P",
    "                                  PPPP",
    "                           PPP     PPPPPP",
    "                              PP BB  PPPPPPPP",
    "                               PPPPBBBPPPPPPPP",
    "                                 PPPBBBBBBBPPPPP",
    "                                    PPBBBBPPPPPPP             H",
    "T                                     PBBPPPPPPEEP          HHH",
    " TT                                     PPPPPPEEKKP      HHHHH",
    "   TT                                   PPPPPPEEKKP  HHHHHHHHH",
    "    TTT                                 PPPPPOEEKKHHHHHHHHHHHH",
    "     TTTT                               PPPPOOEEEEHHHHHHHHHH",
    " T     TTTT                           CCPPPOOOOEEHHHHHHHHHH O",
    "  TTTT  TTTTT                        CCCCPPOOOOOOHHHHH     O",
    "  TTTTTTTTTTTTT  B                 CCCCC  PPOOOOSSSSSSSSSSOO",
    "B  TTTTTTTTT  TTBBBBBBB           CCCCC    PPOOOOOOOOOOOOOO",
    " BBB  TTTTTTT BBBBBBBBBBBBBB    CCCCCC             OOOOOOOO",
    " BBBBBBB TTTTBBBBBBBWWWWWBCBB  CCCCCC                O",
    "  BBBBBBBBB TBBBBBWWWWWWWWWCCCCCCCCC",
    "  BBBBBBBBBBPBBBBWWWWWWWWWWCCCCCCC",
    " PPPPPPPPPPPPPPPPPPWWWWWWWWWCCCCC",
    "    PPPPPPPPPPBPPPPPPPWWWWWBCCCC",
    "      PPPPPPPPPBPPPPPPPPPPBBBB",
    "         P      BPBBBBBBBBBBB",
    "                   BBBBBBBBB",
    "",
    "",
    "",
    "",
    "",
    "",
    "",
    "",
    "",
    "",
}

type startupIntroPoint struct{ x, y int }

// Four distinct strides, including extended and tucked running legs.
var startupIntroLegFrames = [4][2][4]startupIntroPoint{
    {
        {{25, 23}, {19, 27}, {12, 32}, {6, 33}},
        {{28, 23}, {33, 26}, {42, 27}, {49, 32}},
    },
    {
        {{25, 23}, {19, 27}, {23, 29}, {29, 33}},
        {{28, 23}, {34, 26}, {40, 30}, {44, 33}},
    },
    {
        {{25, 23}, {19, 27}, {11, 28}, {5, 30}},
        {{28, 23}, {32, 28}, {29, 32}, {24, 34}},
    },
    {
        {{25, 23}, {22, 28}, {31, 29}, {38, 32}},
        {{28, 23}, {22, 26}, {16, 30}, {10, 32}},
    },
}

func startupIntroLine(pixels [][]byte, a, b startupIntroPoint, ink byte) {
    dx, dy := abs(b.x-a.x), -abs(b.y-a.y)
    sx, sy := -1, -1
    if a.x < b.x {
        sx = 1
    }
    if a.y < b.y {
        sy = 1
    }
    err := dx + dy
    for {
        if a.x >= 0 && a.x < startupIntroSpriteWidth && a.y >= 0 && a.y < startupIntroSpriteHeight {
            pixels[a.y][a.x] = ink
            if a.x+1 < startupIntroSpriteWidth {
                pixels[a.y][a.x+1] = ink
            }
        }
        if a == b {
            break
        }
        twice := 2 * err
        if twice >= dy {
            err += dy
            a.x += sx
        }
        if twice <= dx {
            err += dx
            a.y += sy
        }
    }
}

func startupIntroRaster(frame int) [][]byte {
    pixels := make([][]byte, startupIntroSpriteHeight)
    for i := range pixels {
        pixels[i] = []byte(strings.Repeat(" ", startupIntroSpriteWidth))
    }

    // Legs are behind the body, so their attachment points remain hidden.
    for _, leg := range startupIntroLegFrames[frame%len(startupIntroLegFrames)] {
        for j := 1; j < len(leg); j++ {
            startupIntroLine(pixels, leg[j-1], leg[j], 'L')
        }
        // Long pointed feet emphasize forward motion.
        startupIntroLine(pixels, leg[len(leg)-1], startupIntroPoint{leg[len(leg)-1].x + 4, leg[len(leg)-1].y}, 'L')
    }

    for y, row := range startupRoadRunnerPixels {
        for x := 0; x < len(row); x++ {
            if row[x] != ' ' {
                pixels[y][x] = row[x]
            }
        }
    }
    return pixels
}

func startupIntroColor(pixel byte) lipgloss.Color {
    switch pixel {
    case 'P':
        return lipgloss.Color("#304EA6")
    case 'T':
        return lipgloss.Color("#3476B5")
    case 'B':
        return lipgloss.Color("#398ED2")
    case 'W':
        return lipgloss.Color("#214B94")
    case 'C':
        return lipgloss.Color("#66BDE6")
    case 'H':
        return lipgloss.Color("#F8CE52")
    case 'O', 'L':
        return lipgloss.Color("#F39A3B")
    case 'E':
        return lipgloss.Color("#FFFFFF")
    case 'K':
        return lipgloss.Color("#192034")
    case 'S':
        return lipgloss.Color("#96501C")
    default:
        return lipgloss.Color("#9D8A73") // Dust.
    }
}

// Half-block glyphs allow a detailed, 36-pixel-tall sprite in just 18 rows.
// Empty halves remain transparent to the user's terminal background.
func startupIntroCell(top, bottom byte) string {
    if top == ' ' && bottom == ' ' {
        return " "
    }
    if top == bottom {
        return lipgloss.NewStyle().Foreground(startupIntroColor(top)).Render("█")
    }
    if top == ' ' {
        return lipgloss.NewStyle().Foreground(startupIntroColor(bottom)).Render("▄")
    }
    if bottom == ' ' {
        return lipgloss.NewStyle().Foreground(startupIntroColor(top)).Render("▀")
    }
    return lipgloss.NewStyle().
        Foreground(startupIntroColor(top)).
        Background(startupIntroColor(bottom)).
        Render("▀")
}

// Render only the running bird and dust inside Bubble Tea's alternate screen.
// There is deliberately no brand, title, status, or skip instruction.
func (m pickerModel) startupIntroView() string {
    width, height := m.width, m.height
    if width <= 0 {
        width = 100
    }
    if height <= 0 {
        height = 28
    }
    if width < startupIntroMinWidth || height < startupIntroMinHeight {
        return m.pickerScreen()
    }

    frame := m.introFrame
    pixels := startupIntroRaster(frame)
    // Dust fades backward from the moving bird (and is never rendered as text).
    for i := 0; i < 5; i++ {
        x := 12 - (frame*2+i*5)%13
        y := 29 + (frame+i*2)%6
        if pixels[y][x] == ' ' {
            pixels[y][x] = 'D'
        }
    }

    travel := min(24, width-startupIntroSpriteWidth)
    left := (width-startupIntroSpriteWidth-travel)/2 + frame*travel/(startupIntroFrameCount-1)
    artHeight := startupIntroSpriteHeight/2
    top := max(0, (height-artHeight)/2)
    if frame%4 == 1 && top > 0 {
        top--
    }
    body := make([]string, top, top+artHeight)
    cells := make(map[uint16]string)
    for y := 0; y < startupIntroSpriteHeight; y += 2 {
        var line strings.Builder
        line.WriteString(strings.Repeat(" ", left))
        for x := 0; x < startupIntroSpriteWidth; x++ {
            upper, lower := pixels[y][x], pixels[y+1][x]
            key := uint16(upper)<<8 | uint16(lower)
            glyph, ok := cells[key]
            if !ok {
                glyph = startupIntroCell(upper, lower)
                cells[key] = glyph
            }
            line.WriteString(glyph)
        }
        body = append(body, line.String())
    }
    return pickerScreenFrame(body, nil, width, height)
}
