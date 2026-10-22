package tui

import (
    "strings"
    "time"

    tea "github.com/charmbracelet/bubbletea"
    "github.com/charmbracelet/lipgloss"
)

const (
    startupIntroInterval     = 25 * time.Millisecond
    startupIntroFrameCount   = 24 // Full crossing is 600ms, even on wide terminals.
    startupIntroSpriteWidth  = 64
    startupIntroSpriteHeight = 36
    startupIntroScale        = 3 // Compact ~22×6 sprite; preserve running silhouette.
    startupIntroRenderWidth  = (startupIntroSpriteWidth + startupIntroScale - 1) / startupIntroScale
    startupIntroRenderHeight = startupIntroSpriteHeight / (2 * startupIntroScale)
    startupIntroMinWidth     = startupIntroRenderWidth
    startupIntroMinHeight    = startupIntroRenderHeight
)

type startupIntroTick struct{ frame int }

func startupIntroCmd(frame int) tea.Cmd {
    return tea.Tick(startupIntroInterval, func(time.Time) tea.Msg {
        return startupIntroTick{frame: frame}
    })
}

// Use a fixed number of ticks so a wider terminal does not prolong startup.
// The bird enters from off-screen left, reaches the right edge, then disappears.
func startupIntroFrames(width int) int {
    return startupIntroFrameCount
}

func startupIntroLeft(frame, width int) int {
    if width <= 0 {
        width = 100
    }
    start := -startupIntroRenderWidth + 2
    if frame <= 0 {
        return start
    }
    if frame >= startupIntroFrameCount {
        return width
    }
    return start + (width - start - 1) * frame / (startupIntroFrameCount - 1)
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
    dx, dy := b.x-a.x, a.y-b.y
    if dx < 0 {
        dx = -dx
    }
    if dy > 0 {
        dy = -dy
    }
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

// Collapse each source-pixel block to its dominant nontransparent color,
// preserving the colorful silhouette as the bird runs across the viewport.
func startupIntroReducedPixel(pixels [][]byte, x, y int) byte {
    var colors [256]uint8
    bestColor := byte(' ')
    bestCount := uint8(0)
    for dy := 0; dy < startupIntroScale; dy++ {
        for dx := 0; dx < startupIntroScale; dx++ {
            if y+dy >= startupIntroSpriteHeight || x+dx >= startupIntroSpriteWidth {
                continue // Last sprite column is only one source pixel wide.
            }
            pixel := pixels[y+dy][x+dx]
            if pixel == ' ' {
                continue
            }
            colors[pixel]++
            if colors[pixel] > bestCount {
                bestCount = colors[pixel]
                bestColor = pixel
            }
        }
    }
    return bestColor
}

// Half-block glyphs retain two vertically stacked colors in each terminal
// cell. Empty halves remain transparent to the terminal background.
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

// Only the bird and dust appear, crossing the entire viewport from left to
// right. Clipping is done by cell BEFORE rendering to avoid splitting ANSI
// color escapes as the sprite enters and exits the screen.
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
    // Small dust particles track the sprint instead of leaving any text.
    for i := 0; i < 5; i++ {
        x := 12 - (frame*2+i*5)%13
        y := 29 + (frame+i*2)%6
        if pixels[y][x] == ' ' {
            pixels[y][x] = 'D'
        }
    }

    left := startupIntroLeft(frame, width)
    firstColumn := max(0, -left)
    lastColumn := min(startupIntroRenderWidth, width-left)
    top := max(0, (height-startupIntroRenderHeight)/2)
    if frame%4 == 1 && top > 0 {
        top--
    }
    body := make([]string, top, top+startupIntroRenderHeight)
    cells := make(map[uint16]string)
    for row := 0; row < startupIntroRenderHeight; row++ {
        var line strings.Builder
        line.WriteString(strings.Repeat(" ", max(0, left)))
        for col := firstColumn; col < lastColumn; col++ {
            x := col * startupIntroScale
            y := row * startupIntroScale * 2
            upper := startupIntroReducedPixel(pixels, x, y)
            lower := startupIntroReducedPixel(pixels, x, y+startupIntroScale)
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
