package preview

import (
	"fmt"
	"image"
	"image/color"
	"strings"
)

// AuthzCell is one identity's result for one request row.
type AuthzCell struct {
	Status         int
	Length         int
	SameAsBaseline bool
	AccessDenied   bool
	Broken         bool
	SessionInvalid bool
	FlowID         int64
}

// AuthzRow is one endpoint/request with a cell per identity column.
type AuthzRow struct {
	Label string
	Cells []AuthzCell
}

// AuthzMatrixInput is the plain-data input of RenderAuthzMatrix.
type AuthzMatrixInput struct {
	RunID        string
	BaselineName string
	Cols         []string
	Rows         []AuthzRow
}

const (
	authzMaxCols  = 8
	authzMaxRows  = 40
	authzCellH    = 28
	authzHeaderH  = 32
	authzMinColW  = 104
	authzMaxLabel = 300
	authzFont     = 12
)

type authzLayout struct {
	cols, rows         int // visible
	totalCols, totalRw int
	cells, broken      int // over ALL recorded cells
	hiddenBroken       int
}

// authzVerdict returns the glyph and word for a cell. Priority: session
// invalid, broken, denied, same; anything else is an unclassified difference.
func authzVerdict(c AuthzCell) (glyph, word string) {
	switch {
	case c.SessionInvalid:
		return "S", "session invalid"
	case c.Broken:
		return "!", "broken"
	case c.AccessDenied:
		return "D", "denied"
	case c.SameAsBaseline:
		return "=", "same as baseline"
	}
	return "~", "differs"
}

func authzBrokenTint(p reportPalette) color.RGBA { return blendRGBA(p.paper, p.blocked, 0.14) }

func blendRGBA(bg, fg color.RGBA, a float64) color.RGBA {
	m := func(b, f uint8) uint8 { return uint8(float64(b)*(1-a) + float64(f)*a + 0.5) }
	return color.RGBA{R: m(bg.R, fg.R), G: m(bg.G, fg.G), B: m(bg.B, fg.B), A: 0xff}
}

func authzPlan(in AuthzMatrixInput, rowBudget int) authzLayout {
	l := authzLayout{totalCols: len(in.Cols), totalRw: len(in.Rows)}
	l.cols = min(len(in.Cols), authzMaxCols)
	l.rows = min(len(in.Rows), min(authzMaxRows, max(rowBudget, 1)))
	for ri, r := range in.Rows {
		for ci, c := range r.Cells {
			if ci >= len(in.Cols) {
				break
			}
			l.cells++
			if c.Broken {
				l.broken++
				if ri >= l.rows || ci >= l.cols {
					l.hiddenBroken++
				}
			}
		}
	}
	return l
}

func (l authzLayout) overflow() string {
	var p []string
	if l.totalCols > l.cols {
		p = append(p, fmt.Sprintf("+%d more identities", l.totalCols-l.cols))
	}
	if l.totalRw > l.rows {
		p = append(p, fmt.Sprintf("+%d more rows", l.totalRw-l.rows))
	}
	if len(p) == 0 {
		return ""
	}
	s := strings.Join(p, ", ") + " not drawn"
	if l.hiddenBroken > 0 {
		s += fmt.Sprintf(" (%d broken among them)", l.hiddenBroken)
	}
	return s
}

func authzSummary(l authzLayout) string {
	return fmt.Sprintf("%d broken of %d cells (%d identities x %d rows)", l.broken, l.cells, l.totalCols, l.totalRw)
}

func authzAlt(in AuthzMatrixInput, l authzLayout) string {
	parts := []string{fmt.Sprintf("Authorization differential matrix, %d identities by %d requests, baseline %s", l.totalCols, l.totalRw, orDash(in.BaselineName)), authzSummary(l)}
	var cells []string
	for _, r := range in.Rows {
		for ci, c := range r.Cells {
			if ci < len(in.Cols) && c.Broken {
				cells = append(cells, fmt.Sprintf("%s as %s (status %d)", r.Label, in.Cols[ci], c.Status))
			}
		}
	}
	if len(cells) > 0 {
		extra := 0
		if len(cells) > 20 {
			extra, cells = len(cells)-20, cells[:20]
		}
		s := "Broken access: " + strings.Join(cells, "; ")
		if extra > 0 {
			s += fmt.Sprintf("; and %d more", extra)
		}
		parts = append(parts, s)
	}
	parts = append(parts, l.overflow())
	return AltFromParts(parts...)
}

func orDash(s string) string {
	if strings.TrimSpace(s) == "" {
		return "-"
	}
	return s
}

// authzColWidths sizes columns from header text, shrinking to fit avail.
func authzColWidths(c *canvas, in AuthzMatrixInput, n, avail int) []int {
	w := make([]int, n)
	total := 0
	for i := range w {
		w[i] = max(authzMinColW, c.measure(fontBold, authzFont, in.Cols[i])+24)
		total += w[i]
	}
	if total > avail && n > 0 {
		each := avail / n
		for i := range w {
			w[i] = each
		}
	}
	return w
}

func authzLabelWidth(c *canvas, in AuthzMatrixInput, rows int) int {
	w := c.measure(fontBold, authzFont, "Request") + 24
	for i := 0; i < rows; i++ {
		w = max(w, c.measure(fontSans, authzFont, in.Rows[i].Label)+24)
	}
	return min(w, authzMaxLabel)
}

// RenderAuthzMatrix draws the identity-by-request access matrix.
func RenderAuthzMatrix(in AuthzMatrixInput, o Opts) (Rendered, error) {
	var l authzLayout
	var overflow string
	body := func(rows int) int {
		l = authzPlan(in, rows)
		overflow = l.overflow()
		h := 16 + 30 + authzHeaderH + max(l.rows, 1)*authzCellH + 12 + 24 + 16
		if overflow != "" {
			h += 24
		}
		return h
	}
	fr := frame{
		Title:      "Authz differential matrix",
		Provenance: "Run " + orDash(in.RunID) + "; baseline " + orDash(in.BaselineName),
		BodyHeight: body,
		Draw: func(c *canvas, area image.Rectangle, rows int) {
			l = authzPlan(in, rows)
			drawAuthz(c, area, in, l)
		},
	}
	png, w, h, err := renderFrame(o, fr)
	if err != nil {
		return Rendered{}, err
	}
	l = authzPlan(in, authzMaxRows)
	return Rendered{PNG: png, Alt: authzAlt(in, l), Summary: authzSummary(l), Kind: KindAuthzMatrix, Width: w, Height: h}, nil
}

func drawAuthz(c *canvas, area image.Rectangle, in AuthzMatrixInput, l authzLayout) {
	p := c.pal
	x0, y := area.Min.X+frameGut, area.Min.Y+16
	avail := area.Dx() - 2*frameGut
	sumCol := p.success
	if l.broken > 0 {
		sumCol = p.blocked
	}
	c.rect(x0, y, 4, 22, sumCol)
	tw := c.text(x0+12, y+2, fmt.Sprintf("%d broken of %d", l.broken, l.cells), fontBold, 18, p.ink)
	c.text(x0+12+tw+10, y+6, "cells | baseline "+orDash(in.BaselineName), fontSans, authzFont, p.muted)
	y += 30
	if l.cols == 0 || l.rows == 0 {
		c.text(x0, y+8, "No identities or requests recorded for this run.", fontSans, 14, p.muted)
		return
	}
	labelW := min(authzLabelWidth(c, in, l.rows), max(160, avail-l.cols*authzMinColW))
	colW := authzColWidths(c, in, l.cols, avail-labelW)
	widths := append([]int{labelW}, colW...)
	tableW := 0
	for _, w := range widths {
		tableW += w
	}
	c.rect(x0, y, tableW, authzHeaderH, p.panel)
	c.text(x0+12, y+9, "Request", fontBold, authzFont, p.ink)
	cx := x0 + labelW
	for i := 0; i < l.cols; i++ {
		c.text(cx+12, y+9, c.truncate(fontBold, authzFont, in.Cols[i], colW[i]-16), fontBold, authzFont, p.ink)
		cx += colW[i]
	}
	ry := y + authzHeaderH
	for r := 0; r < l.rows; r++ {
		row := in.Rows[r]
		c.text(x0+12, ry+8, c.truncate(fontSans, authzFont, row.Label, labelW-20), fontSans, authzFont, p.ink)
		cx = x0 + labelW
		for i := 0; i < l.cols; i++ {
			if i < len(row.Cells) {
				drawAuthzCell(c, cx, ry, colW[i], row.Cells[i])
			}
			cx += colW[i]
		}
		ry += authzCellH
	}
	c.tableGrid(x0, y, widths, authzCellH, 0)
	c.tableGrid(x0, y+authzHeaderH, widths, authzCellH, l.rows)
	c.rect(x0, y, tableW+1, 1, p.grid)
	c.rect(x0, y+authzHeaderH, tableW+1, 1, p.grid)
	y = ry + 12
	if ov := l.overflow(); ov != "" {
		c.text(x0, y, ov, fontBold, authzFont, p.ink)
		y += 24
	}
	c.legend(x0, y, x0+avail, []legendItem{
		{"= same as baseline", p.success}, {"D denied", p.redirect}, {"! broken access", p.blocked}, {"S session invalid", p.client}, {"~ differs (unclassified)", p.muted},
	})
}

func drawAuthzCell(c *canvas, x, y, w int, cell AuthzCell) {
	p := c.pal
	glyph, _ := authzVerdict(cell)
	gc := p.ink
	switch glyph {
	case "!":
		c.rect(x+1, y+1, w-1, authzCellH-1, authzBrokenTint(p))
		c.strokeRect(x+1, y+1, w-1, authzCellH-1, p.blocked)
		gc = p.blocked
	case "S":
		gc = p.client
	case "D":
		gc = p.redirect
	case "=":
		gc = p.success
	}
	gx := x + 10
	gx += c.text(gx, y+7, glyph, fontBold, 14, gc) + 8
	st := "-"
	if cell.Status > 0 {
		st = fmt.Sprint(cell.Status)
	}
	gx += c.text(gx, y+8, st, fontBold, authzFont, p.statusColor(cell.Status)) + 8
	c.text(gx, y+8, c.truncate(fontSans, authzFont, fmt.Sprintf("%d B", cell.Length), x+w-gx-4), fontSans, authzFont, p.muted)
}
