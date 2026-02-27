package basket

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"text/tabwriter"
	"time"

	"github.com/nover/intermarche-mcp/internal/api"
)

// Event describes a cart mutation.
type Event struct {
	Type     string `json:"type"`
	ItemID   int    `json:"itemId"`
	Quantity int    `json:"quantity"`
	DateTime string `json:"dateTime"`
}

// syncRequest is the POST body for the cart sync endpoint.
type syncRequest struct {
	CustomerDateTime    string  `json:"customerDateTime"`
	Events              []Event `json:"events"`
	LastSynchronizedCart string  `json:"lastSynchronizedCart,omitempty"`
}

// CartItem represents a product in the cart response.
type CartItem struct {
	ID       string  `json:"id"`
	Quantity int     `json:"quantity"`
	Price    float64 `json:"price"`
	Amount   float64 `json:"amount"`
	Item     struct {
		EAN       string  `json:"produitEan13"`
		Title     string  `json:"libelle"`
		Brand     string  `json:"marque"`
		Price     float64 `json:"prix"`
		Packaging string  `json:"conditionnement"`
	} `json:"item"`
}

// SubCart is a seller sub-cart in the response.
type SubCart struct {
	Items []CartItem `json:"items"`
}

// Cart is the API response from a cart sync.
type Cart struct {
	ID                  string    `json:"id"`
	Carts               []SubCart `json:"carts"`
	ItemsNumber         int       `json:"itemsNumber"`
	Amount              float64   `json:"amount"`
	SynchronizeDateTime string    `json:"synchronizeDateTime"`
}

// lookupProduct resolves an EAN barcode to its internal product ID via the product API.
func lookupProduct(ctx context.Context, client *api.Client, storeID, ean string) (int, string, error) {
	body, err := json.Marshal(map[string][]string{"eans": {ean}})
	if err != nil {
		return 0, "", err
	}

	url := fmt.Sprintf("%s/produits/v3/stores/%s/products/byEans", api.BaseURL, storeID)
	req, err := http.NewRequestWithContext(ctx, "POST", url, bytes.NewReader(body))
	if err != nil {
		return 0, "", err
	}

	resp, err := client.Do(req)
	if err != nil {
		return 0, "", fmt.Errorf("product lookup: %w", err)
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusOK {
		return 0, "", fmt.Errorf("product lookup failed with status %d", resp.StatusCode)
	}

	var result struct {
		Products []struct {
			Identifier int    `json:"identifier"`
			EAN        string `json:"produitEan13"`
			Title      string `json:"libelle"`
		} `json:"products"`
	}
	if err := json.NewDecoder(resp.Body).Decode(&result); err != nil {
		return 0, "", fmt.Errorf("decoding product lookup: %w", err)
	}
	if len(result.Products) == 0 {
		return 0, "", fmt.Errorf("product not found: %s", ean)
	}
	p := result.Products[0]
	return p.Identifier, p.Title, nil
}

// syncCart sends a cart sync request with the given events and returns the updated cart.
func syncCart(ctx context.Context, client *api.Client, storeID, customerID string, events []Event) (*Cart, error) {
	now := time.Now().Format("2006-01-02T15:04:05-07:00")
	body := syncRequest{
		CustomerDateTime: now,
		Events:           events,
	}

	data, err := json.Marshal(body)
	if err != nil {
		return nil, err
	}

	// The cart API requires the store ID to be zero-padded to 5 digits.
	paddedStore := fmt.Sprintf("%05s", storeID)
	url := fmt.Sprintf("%s/panier/v1/stores/%s/carts?customerId=%s&actions=VALUATION,ANIMATIONS",
		api.BaseURL, paddedStore, customerID)

	req, err := http.NewRequestWithContext(ctx, "POST", url, bytes.NewReader(data))
	if err != nil {
		return nil, err
	}

	resp, err := client.Do(req)
	if err != nil {
		return nil, fmt.Errorf("cart request: %w", err)
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusOK {
		var errResp struct {
			Errors []struct {
				Message string `json:"message"`
				Code    string `json:"code"`
			} `json:"errors"`
		}
		json.NewDecoder(resp.Body).Decode(&errResp)
		if len(errResp.Errors) > 0 {
			return nil, fmt.Errorf("cart update failed (%d): %s", resp.StatusCode, errResp.Errors[0].Message)
		}
		return nil, fmt.Errorf("cart update failed with status %d", resp.StatusCode)
	}

	var cart Cart
	if err := json.NewDecoder(resp.Body).Decode(&cart); err != nil {
		return nil, fmt.Errorf("decoding cart response: %w", err)
	}

	return &cart, nil
}

// FindItem returns the CartItem matching the given EAN in the cart, or nil if not found.
func (c *Cart) FindItem(ean string) *CartItem {
	for i := range c.Carts {
		for j := range c.Carts[i].Items {
			if c.Carts[i].Items[j].Item.EAN == ean {
				return &c.Carts[i].Items[j]
			}
		}
	}
	return nil
}

// GetCart fetches the current cart state (sync with no events).
func GetCart(ctx context.Context, client *api.Client, storeID, customerID string) (*Cart, error) {
	return syncCart(ctx, client, storeID, customerID, []Event{})
}

// AddItem adds a product to the cart.
// quantity is the delta (+1 to add one, etc.).
// Returns the updated cart and the product title.
func AddItem(ctx context.Context, client *api.Client, storeID, customerID, ean string, quantity int) (*Cart, string, error) {
	productID, title, err := lookupProduct(ctx, client, storeID, ean)
	if err != nil {
		return nil, "", err
	}

	now := time.Now().Format("2006-01-02T15:04:05-07:00")
	cart, err := syncCart(ctx, client, storeID, customerID, []Event{
		{
			Type:     "QUANTITY",
			ItemID:   productID,
			Quantity: quantity,
			DateTime: now,
		},
	})
	if err != nil {
		return nil, "", err
	}
	return cart, title, nil
}

// PrintCart prints the cart contents in the given format ("table" or "json").
func PrintCart(w io.Writer, cart *Cart, format string) {
	if format == "json" {
		json.NewEncoder(w).Encode(cart)
		return
	}

	if cart.ItemsNumber == 0 {
		fmt.Fprintln(w, "Basket is empty.")
		return
	}

	fmt.Fprintf(w, "%d items — %.2f €\n\n", cart.ItemsNumber, cart.Amount)

	tw := tabwriter.NewWriter(w, 0, 0, 2, ' ', 0)
	fmt.Fprintln(tw, "QTY\tUNIT\tAMOUNT\tBRAND\tNAME")
	fmt.Fprintln(tw, "───\t────\t──────\t─────\t────")
	for _, sub := range cart.Carts {
		for _, item := range sub.Items {
			fmt.Fprintf(tw, "%d\t%.2f €\t%.2f €\t%s\t%s\n",
				item.Quantity, item.Price, item.Amount, item.Item.Brand, item.Item.Title)
		}
	}
	tw.Flush()
}

// RemoveItem removes a product from the cart.
// If quantity is 0, removes all of this product. Otherwise removes the given amount.
// Returns the updated cart and the product title.
func RemoveItem(ctx context.Context, client *api.Client, storeID, customerID, ean string, quantity int) (*Cart, string, error) {
	productID, title, err := lookupProduct(ctx, client, storeID, ean)
	if err != nil {
		return nil, "", err
	}

	delta := -quantity
	if quantity == 0 {
		// Fetch current cart to find item quantity.
		cart, err := GetCart(ctx, client, storeID, customerID)
		if err != nil {
			return nil, "", fmt.Errorf("fetching cart: %w", err)
		}
		item := cart.FindItem(ean)
		if item == nil {
			return nil, "", fmt.Errorf("product %s (%s) is not in the basket", ean, title)
		}
		delta = -item.Quantity
	}

	now := time.Now().Format("2006-01-02T15:04:05-07:00")
	cart, err := syncCart(ctx, client, storeID, customerID, []Event{
		{
			Type:     "QUANTITY",
			ItemID:   productID,
			Quantity: delta,
			DateTime: now,
		},
	})
	if err != nil {
		return nil, "", err
	}
	return cart, title, nil
}
