package store

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"net/url"

	"github.com/nover/intermarche-mcp/internal/api"
)

// Store holds the details for an Intermarché point de vente.
type Store struct {
	Ref     string `json:"ref"`
	Name    string `json:"name"`
	Format  string `json:"format"` // "SUPER ALIMENTAIRE", "HYPER", "CONTACT", …
	Address string `json:"address"`
	ZipCode string `json:"zipCode"`
	City    string `json:"city"`
}

// searchResponse is the shape returned by /pdvs/v1/stores?scope=AUTOCOMPLETE.
type searchResponse struct {
	StoreInfoList []storeInfo `json:"storeInfoList"`
}

type storeInfo struct {
	StoreID    string `json:"storeId"`
	ModelLabel string `json:"modelLabel"`
	Address    struct {
		Address   string `json:"address"`
		PostCode  string `json:"postCode"`
		TownLabel string `json:"townLabel"`
	} `json:"address"`
}

// Search finds Intermarché stores matching a postal code or city name.
func Search(ctx context.Context, client *api.Client, query string) ([]Store, error) {
	u := fmt.Sprintf("%s/pdvs/v1/stores?scope=AUTOCOMPLETE&postalCodeOrCity=%s",
		api.BaseURL, url.QueryEscape(query))

	req, err := http.NewRequestWithContext(ctx, "GET", u, nil)
	if err != nil {
		return nil, err
	}

	resp, err := client.Do(req)
	if err != nil {
		return nil, fmt.Errorf("store search request: %w", err)
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusOK {
		return nil, fmt.Errorf("store search failed with status %d", resp.StatusCode)
	}

	var sr searchResponse
	if err := json.NewDecoder(resp.Body).Decode(&sr); err != nil {
		return nil, fmt.Errorf("decoding store search response: %w", err)
	}

	stores := make([]Store, len(sr.StoreInfoList))
	for i, info := range sr.StoreInfoList {
		stores[i] = Store{
			Ref:     info.StoreID,
			Format:  info.ModelLabel,
			Address: info.Address.Address,
			ZipCode: info.Address.PostCode,
			City:    info.Address.TownLabel,
		}
	}
	return stores, nil
}
