package search

import (
	"encoding/json"
	"fmt"
	"io"
	"text/tabwriter"
)

// PrintResults prints search results in the given format ("table" or "json").
func PrintResults(w io.Writer, results *Results, query string, format string) {
	if format == "json" {
		json.NewEncoder(w).Encode(results)
		return
	}

	fmt.Fprintf(w, "Search: %q — %d results (page %d/%d)\n\n",
		query, results.Total, results.Page, results.TotalPages)

	if len(results.Products) == 0 {
		fmt.Fprintln(w, "No products found.")
		return
	}

	tw := tabwriter.NewWriter(w, 0, 0, 2, ' ', 0)
	fmt.Fprintln(tw, "EAN\tPRICE\tUNIT PRICE\tBRAND\tNAME")
	fmt.Fprintln(tw, "───\t─────\t──────────\t─────\t────")
	for _, p := range results.Products {
		title := truncate(p.Title, 50)
		brand := truncate(p.Brand, 15)
		unitPrice := ""
		if p.UnitPrice > 0 && p.UnitPriceLabel != "" {
			unitPrice = fmt.Sprintf("%.2f %s", p.UnitPrice, p.UnitPriceLabel)
		}
		fmt.Fprintf(tw, "%s\t%.2f €\t%s\t%s\t%s\n",
			p.EAN, p.Price, unitPrice, brand, title)
	}
	tw.Flush()
}

func truncate(s string, max int) string {
	runes := []rune(s)
	if len(runes) <= max {
		return s
	}
	return string(runes[:max-1]) + "…"
}
