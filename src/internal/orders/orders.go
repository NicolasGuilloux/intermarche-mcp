package orders

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"

	"github.com/nover/intermarche-mcp/internal/api"
)

type Slot struct {
	Date      string `json:"date"`
	StartTime string `json:"heureDebut"`
	EndTime   string `json:"heureFin"`
}

type Order struct {
	OrderNumber   string  `json:"numeroCommande"`
	StoreName     string  `json:"nomMagasin"`
	DeliveryType  string  `json:"typeLivraison"`
	Status        string  `json:"statutCommande"`
	StoreID       string  `json:"identifiantPdv"`
	TotalAmount   float64 `json:"totalCommandeAPayer"`
	Currency      string  `json:"devise"`
	Discount      float64 `json:"totalBri"`
	CreatedAt     string  `json:"dateCreation"`
	DeliveredAt   string  `json:"dateLivraison"`
	Slot          Slot    `json:"creneauSouhaite"`
	NumProducts   int     `json:"nbProduits"`
	PaymentType   string  `json:"typeDePaiement"`
	PaymentMethod string  `json:"moyenDePaiement"`
}

type Article struct {
	EAN       string  `json:"idProduit"`
	Title     string  `json:"libelle"`
	Brand     string  `json:"marque"`
	Price     float64 `json:"prix"`
	Quantity  float64 `json:"quantite"`
	Amount    float64 `json:"montant"`
	Promotion string  `json:"promotion"`
}

type OrderDetail struct {
	Order
	Articles []Article `json:"articles"`
}

type listResponse struct {
	Commandes []Order `json:"commandes"`
}

// ListOrders fetches orders for the given user and store.
// It queries both active (enCours=true) and historical (historique=true) orders.
func ListOrders(ctx context.Context, client *api.Client, userID, pdvRef string) ([]Order, error) {
	url := fmt.Sprintf(
		"%s/commandes/v2/consommateurs/%s/commandes?enCours=true&historique=true&nbMois=3&nombre=100&numeroPDV=%s&withPdv=true",
		api.BaseURL, userID, pdvRef,
	)

	req, err := http.NewRequestWithContext(ctx, "GET", url, nil)
	if err != nil {
		return nil, err
	}

	resp, err := client.Do(req)
	if err != nil {
		return nil, fmt.Errorf("listing orders: %w", err)
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusOK {
		return nil, fmt.Errorf("list orders failed with status %d", resp.StatusCode)
	}

	var raw listResponse
	if err := json.NewDecoder(resp.Body).Decode(&raw); err != nil {
		return nil, fmt.Errorf("decoding orders: %w", err)
	}

	return raw.Commandes, nil
}

// GetOrder fetches the detail of a single order including its articles.
func GetOrder(ctx context.Context, client *api.Client, userID, pdvRef, orderNumber string) (*OrderDetail, error) {
	url := fmt.Sprintf(
		"%s/commandes/v1/consommateurs/%s/commandes/%s?numeroPDV=%s",
		api.BaseURL, userID, orderNumber, pdvRef,
	)

	req, err := http.NewRequestWithContext(ctx, "GET", url, nil)
	if err != nil {
		return nil, err
	}

	resp, err := client.Do(req)
	if err != nil {
		return nil, fmt.Errorf("fetching order detail: %w", err)
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusOK {
		return nil, fmt.Errorf("order detail failed with status %d", resp.StatusCode)
	}

	var detail OrderDetail
	if err := json.NewDecoder(resp.Body).Decode(&detail); err != nil {
		return nil, fmt.Errorf("decoding order detail: %w", err)
	}

	return &detail, nil
}
