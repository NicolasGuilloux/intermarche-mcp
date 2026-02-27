package search

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"net/http"

	"github.com/nover/intermarche-mcp/internal/api"
)

type Product struct {
	EAN           string  `json:"produitEan13"`
	ID            int     `json:"identifier"`
	Title         string  `json:"libelle"`
	Brand         string  `json:"marque"`
	Price         float64 `json:"prix"`
	UnitPrice     float64 `json:"prixKg"`
	UnitPriceLabel string `json:"unitePrixVente"`
	Stock         int     `json:"stock"`
	Packaging     string  `json:"conditionnement"`
	MaxQty        int     `json:"qteMaxPanier"`
}

type unitPrice struct {
	Value string `json:"value"`
}

type Results struct {
	Products   []Product `json:"products"`
	Total      int       `json:"total"`
	Page       int       `json:"page"`
	TotalPages int       `json:"totalPages"`
}

// searchRequest is the POST body for the search API.
type searchRequest struct {
	Keyword   string        `json:"keyword"`
	Page      int           `json:"page"`
	Size      int           `json:"size"`
	Filters   []interface{} `json:"filtres"`
	Sort      string        `json:"tri"`
	SortOrder string        `json:"ordreTri"`
	Catalog   string        `json:"catalog"`
}

// searchResponse maps the raw API response.
type searchResponse struct {
	SearchResultsMetaData struct {
		ResultNbre        int `json:"resultNbre"`
		ResultNbrePerPage int `json:"resultNbrePerPage"`
		CurrentPage       int `json:"currentPage"`
		TotalPageNbre     int `json:"totalPageNbre"`
	} `json:"searchResultsMetaData"`
	Produits []rawProduct `json:"produits"`
}

type rawProduct struct {
	EAN            string   `json:"produitEan13"`
	ID             int      `json:"identifier"`
	Title          string   `json:"libelle"`
	Brand          string   `json:"marque"`
	Price          float64  `json:"prix"`
	UnitPrice      float64  `json:"prixKg"`
	UnitPriceLabel unitPrice `json:"unitePrixVente"`
	Stock          int      `json:"stock"`
	Packaging      string   `json:"conditionnement"`
	MaxQty         int      `json:"qteMaxPanier"`
}

// Search queries the Intermarché product catalog by keyword.
func Search(ctx context.Context, client *api.Client, pdvRef, query string, page, size int) (*Results, error) {
	body := searchRequest{
		Keyword:   query,
		Page:      page,
		Size:      size,
		Filters:   []interface{}{},
		Sort:      "pertinence",
		SortOrder: "CROISSANT",
		Catalog:   "courses-en-ligne",
	}

	data, err := json.Marshal(body)
	if err != nil {
		return nil, err
	}

	url := fmt.Sprintf("%s/produits/v4/pdvs/%s/products/byKeywordAndCategory", api.BaseURL, pdvRef)
	req, err := http.NewRequestWithContext(ctx, "POST", url, bytes.NewReader(data))
	if err != nil {
		return nil, err
	}

	resp, err := client.Do(req)
	if err != nil {
		return nil, fmt.Errorf("search request: %w", err)
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
			return nil, fmt.Errorf("search failed (%d): %s", resp.StatusCode, errResp.Errors[0].Message)
		}
		return nil, fmt.Errorf("search failed with status %d", resp.StatusCode)
	}

	var raw searchResponse
	if err := json.NewDecoder(resp.Body).Decode(&raw); err != nil {
		return nil, fmt.Errorf("decoding search response: %w", err)
	}

	products := make([]Product, len(raw.Produits))
	for i, p := range raw.Produits {
		products[i] = Product{
			EAN:            p.EAN,
			ID:             p.ID,
			Title:          p.Title,
			Brand:          p.Brand,
			Price:          p.Price,
			UnitPrice:      p.UnitPrice,
			UnitPriceLabel: p.UnitPriceLabel.Value,
			Stock:          p.Stock,
			Packaging:      p.Packaging,
			MaxQty:         p.MaxQty,
		}
	}

	return &Results{
		Products:   products,
		Total:      raw.SearchResultsMetaData.ResultNbre,
		Page:       raw.SearchResultsMetaData.CurrentPage,
		TotalPages: raw.SearchResultsMetaData.TotalPageNbre,
	}, nil
}
