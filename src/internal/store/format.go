package store

import (
	"encoding/json"
	"fmt"
	"io"
	"text/tabwriter"
)

// PrintResults displays a list of stores to w in the given format ("table" or "json").
func PrintResults(w io.Writer, stores []Store, query string, format string) {
	if format == "json" {
		json.NewEncoder(w).Encode(stores)
		return
	}

	if len(stores) == 0 {
		fmt.Fprintf(w, "No stores found for %q\n", query)
		return
	}

	fmt.Fprintf(w, "Found %d store(s) for %q:\n\n", len(stores), query)
	tw := tabwriter.NewWriter(w, 0, 0, 2, ' ', 0)
	fmt.Fprintln(tw, "REF\tFORMAT\tADDRESS\tCITY")
	fmt.Fprintln(tw, "───\t──────\t───────\t────")
	for _, s := range stores {
		fmt.Fprintf(tw, "%s\t%s\t%s\t%s %s\n", s.Ref, s.Format, s.Address, s.ZipCode, s.City)
	}
	tw.Flush()
}
