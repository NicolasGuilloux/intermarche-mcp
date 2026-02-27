package orders

import (
	"encoding/json"
	"fmt"
	"io"
	"text/tabwriter"
)

// PrintOrders prints a list of orders in the given format ("table" or "json").
func PrintOrders(w io.Writer, orders []Order, format string) {
	if format == "json" {
		json.NewEncoder(w).Encode(orders)
		return
	}

	fmt.Fprintf(w, "%d orders\n\n", len(orders))

	if len(orders) == 0 {
		fmt.Fprintln(w, "No orders found.")
		return
	}

	tw := tabwriter.NewWriter(w, 0, 0, 2, ' ', 0)
	fmt.Fprintln(tw, "DATE\tORDER #\tSTATUS\tITEMS\tTOTAL\tSTORE")
	fmt.Fprintln(tw, "────\t───────\t──────\t─────\t─────\t─────")
	for _, o := range orders {
		date := truncateDate(o.CreatedAt)
		store := truncate(o.StoreName, 25)
		fmt.Fprintf(tw, "%s\t%s\t%s\t%d\t%.2f €\t%s\n",
			date, o.OrderNumber, o.Status, o.NumProducts, o.TotalAmount, store)
	}
	tw.Flush()
}

// PrintOrderDetail prints the order header and a table of articles in the given format.
func PrintOrderDetail(w io.Writer, detail *OrderDetail, format string) {
	if format == "json" {
		json.NewEncoder(w).Encode(detail)
		return
	}

	fmt.Fprintf(w, "Order #%s\n", detail.OrderNumber)
	fmt.Fprintf(w, "Status: %s\n", detail.Status)
	fmt.Fprintf(w, "Store:  %s\n", detail.StoreName)
	fmt.Fprintf(w, "Date:   %s\n", truncateDate(detail.CreatedAt))
	if detail.Slot.Date != "" {
		fmt.Fprintf(w, "Slot:   %s %s–%s\n", detail.Slot.Date, detail.Slot.StartTime, detail.Slot.EndTime)
	}
	fmt.Fprintf(w, "Items:  %d\n", detail.NumProducts)
	fmt.Fprintf(w, "Total:  %.2f €\n", detail.TotalAmount)
	if detail.Discount > 0 {
		fmt.Fprintf(w, "Discount: -%.2f €\n", detail.Discount)
	}
	fmt.Println()

	if len(detail.Articles) == 0 {
		fmt.Fprintln(w, "No articles.")
		return
	}

	tw := tabwriter.NewWriter(w, 0, 0, 2, ' ', 0)
	fmt.Fprintln(tw, "EAN\tQTY\tUNIT\tAMOUNT\tBRAND\tNAME")
	fmt.Fprintln(tw, "───\t───\t────\t──────\t─────\t────")
	for _, a := range detail.Articles {
		title := truncate(a.Title, 50)
		brand := truncate(a.Brand, 30)
		qty := formatQty(a.Quantity)
		unitPrice := a.Amount / a.Quantity
		fmt.Fprintf(tw, "%s\t%s\t%.2f €\t%.2f €\t%s\t%s\n",
			a.EAN, qty, unitPrice, a.Amount, brand, title)
	}
	tw.Flush()
}

// formatQty formats a quantity: "2" for whole numbers, "1.234" for fractional (by-weight items).
func formatQty(q float64) string {
	if q == float64(int(q)) {
		return fmt.Sprintf("%d", int(q))
	}
	return fmt.Sprintf("%.3f", q)
}

// truncateDate extracts the date portion (first 10 chars) from an ISO timestamp.
func truncateDate(s string) string {
	if len(s) >= 10 {
		return s[:10]
	}
	return s
}

func truncate(s string, max int) string {
	runes := []rune(s)
	if len(runes) <= max {
		return s
	}
	return string(runes[:max-1]) + "…"
}
