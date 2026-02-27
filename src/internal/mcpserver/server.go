package mcpserver

import (
	"bytes"
	"context"
	"fmt"
	"net/http"
	"sync"

	"github.com/mark3labs/mcp-go/mcp"
	"github.com/mark3labs/mcp-go/server"

	"github.com/nover/intermarche-mcp/internal/api"
	"github.com/nover/intermarche-mcp/internal/auth"
	"github.com/nover/intermarche-mcp/internal/basket"
	"github.com/nover/intermarche-mcp/internal/config"
	"github.com/nover/intermarche-mcp/internal/orders"
	"github.com/nover/intermarche-mcp/internal/search"
	"github.com/nover/intermarche-mcp/internal/solver"
	"github.com/nover/intermarche-mcp/internal/store"
)

// apiTransport is the transport contract the MCP server depends on: an HTTP
// round-tripper that can report liveness and be closed.
type apiTransport interface {
	http.RoundTripper
	IsAlive() bool
	Close() error
}

// dialTransport builds the API transport. Every request is wrapped through the
// Salamoonder solver (internal/solver), which clears Datadome over the network
// and caches the cleared cookie. It is the only transport.
func dialTransport(_ context.Context) (apiTransport, error) {
	return solver.New()
}

// state holds shared state across MCP tool calls (lazy transport).
type state struct {
	mu        sync.Mutex
	transport apiTransport
}

func (s *state) getTransport(ctx context.Context) (apiTransport, error) {
	s.mu.Lock()
	defer s.mu.Unlock()

	if s.transport != nil && !s.transport.IsAlive() {
		_ = s.transport.Close()
		s.transport = nil
	}

	if s.transport == nil {
		t, err := dialTransport(ctx)
		if err != nil {
			return nil, err
		}
		s.transport = t
	}

	return s.transport, nil
}

// RoundTrip implements http.RoundTripper, delegating to the cached solver
// transport (lazily dialed). The solver handles Datadome 403s internally.
func (s *state) RoundTrip(req *http.Request) (*http.Response, error) {
	t, err := s.getTransport(req.Context())
	if err != nil {
		return nil, err
	}
	return t.RoundTrip(req)
}

func (s *state) apiClient(ctx context.Context, token string) (*api.Client, error) {
	// Eagerly build the transport before handing back a client.
	if _, err := s.getTransport(ctx); err != nil {
		return nil, err
	}
	// Pass s itself as the RoundTripper so all requests go through the
	// reconnect-on-error logic above.
	client := api.New(token, s)
	if token != "" {
		client.RefreshFunc = func() (string, error) { return auth.ForceRefresh(ctx) }
	}
	return client, nil
}

func (s *state) authedClient(ctx context.Context) (*api.Client, string, error) {
	token, err := auth.GetToken(ctx)
	if err != nil {
		return nil, "", fmt.Errorf("not logged in — run 'intermarche-mcp login' first")
	}

	customerID, err := auth.UserID(token)
	if err != nil {
		return nil, "", fmt.Errorf("reading user ID from token: %w", err)
	}

	client, err := s.apiClient(ctx, token)
	if err != nil {
		return nil, "", err
	}
	return client, customerID, nil
}

func textResult(text string) *mcp.CallToolResult {
	return &mcp.CallToolResult{
		Content: []mcp.Content{
			mcp.TextContent{Type: "text", Text: text},
		},
	}
}

func errResult(msg string) *mcp.CallToolResult {
	return &mcp.CallToolResult{
		Content: []mcp.Content{
			mcp.TextContent{Type: "text", Text: msg},
		},
		IsError: true,
	}
}

// NewServer creates and configures the MCP server with all Intermarché tools.
func NewServer() *server.MCPServer {
	s := &state{}

	mcpServer := server.NewMCPServer(
		"intermarche-mcp",
		"1.0.0",
	)

	mcpServer.AddTool(
		mcp.NewTool("store_search",
			mcp.WithDescription("Search Intermarché stores by city name or postal code"),
			mcp.WithString("query", mcp.Required(), mcp.Description("City name or postal code")),
		),
		s.handleStoreSearch,
	)

	mcpServer.AddTool(
		mcp.NewTool("store_set",
			mcp.WithDescription("Set the active Intermarché store by PDV reference number"),
			mcp.WithString("pdv_ref", mcp.Required(), mcp.Description("Store PDV reference (e.g. '830')")),
		),
		s.handleStoreSet,
	)

	mcpServer.AddTool(
		mcp.NewTool("store_get",
			mcp.WithDescription("Get the currently selected Intermarché store"),
		),
		s.handleStoreGet,
	)

	mcpServer.AddTool(
		mcp.NewTool("search_products",
			mcp.WithDescription("Search the Intermarché product catalog. A store must be selected first."),
			mcp.WithString("query", mcp.Required(), mcp.Description("Product search query")),
			mcp.WithNumber("page", mcp.Description("Page number (default: 1)")),
			mcp.WithNumber("size", mcp.Description("Results per page (default: 20)")),
		),
		s.handleSearchProducts,
	)

	mcpServer.AddTool(
		mcp.NewTool("basket_get",
			mcp.WithDescription("Get current shopping cart contents. Requires login and a selected store."),
		),
		s.handleBasketGet,
	)

	mcpServer.AddTool(
		mcp.NewTool("basket_add",
			mcp.WithDescription("Add a product to the shopping cart by EAN barcode. Requires login and a selected store."),
			mcp.WithString("ean", mcp.Required(), mcp.Description("Product EAN-13 barcode")),
			mcp.WithNumber("quantity", mcp.Description("Quantity to add (default: 1)")),
		),
		s.handleBasketAdd,
	)

	mcpServer.AddTool(
		mcp.NewTool("basket_remove",
			mcp.WithDescription("Remove a product from the shopping cart by EAN barcode. Quantity 0 removes all. Requires login and a selected store."),
			mcp.WithString("ean", mcp.Required(), mcp.Description("Product EAN-13 barcode")),
			mcp.WithNumber("quantity", mcp.Description("Quantity to remove (0 = remove all, default: 0)")),
		),
		s.handleBasketRemove,
	)

	mcpServer.AddTool(
		mcp.NewTool("orders_list",
			mcp.WithDescription("List recent orders. Requires login and a selected store."),
		),
		s.handleOrdersList,
	)

	mcpServer.AddTool(
		mcp.NewTool("order_detail",
			mcp.WithDescription("Get detailed information about a specific order including all articles. Requires login and a selected store."),
			mcp.WithString("order_number", mcp.Required(), mcp.Description("Order number")),
		),
		s.handleOrderDetail,
	)

	return mcpServer
}

// --- Tool handlers ---

func (s *state) handleStoreSearch(ctx context.Context, req mcp.CallToolRequest) (*mcp.CallToolResult, error) {
	query, err := req.RequireString("query")
	if err != nil {
		return errResult("missing required parameter: query"), nil
	}

	client, err := s.apiClient(ctx, "")
	if err != nil {
		return errResult(fmt.Sprintf("failed to connect: %v", err)), nil
	}

	results, err := store.Search(ctx, client, query)
	if err != nil {
		return errResult(fmt.Sprintf("store search failed: %v", err)), nil
	}

	if len(results) == 0 {
		return textResult(fmt.Sprintf("No stores found for '%s'", query)), nil
	}

	var buf bytes.Buffer
	store.PrintResults(&buf, results, query, "json")
	return textResult(buf.String()), nil
}

func (s *state) handleStoreSet(_ context.Context, req mcp.CallToolRequest) (*mcp.CallToolResult, error) {
	pdvRef, err := req.RequireString("pdv_ref")
	if err != nil {
		return errResult("missing required parameter: pdv_ref"), nil
	}

	if err := config.SaveStore(&config.StoreConfig{PdvRef: pdvRef}); err != nil {
		return errResult(fmt.Sprintf("failed to save store: %v", err)), nil
	}

	return textResult(fmt.Sprintf("Store set to %s", pdvRef)), nil
}

func (s *state) handleStoreGet(_ context.Context, _ mcp.CallToolRequest) (*mcp.CallToolResult, error) {
	sc, err := config.LoadStore()
	if err != nil {
		return errResult("no store selected — use store_set to select one"), nil
	}
	return textResult(fmt.Sprintf("Current store: %s", sc.PdvRef)), nil
}

func (s *state) handleSearchProducts(ctx context.Context, req mcp.CallToolRequest) (*mcp.CallToolResult, error) {
	query, err := req.RequireString("query")
	if err != nil {
		return errResult("missing required parameter: query"), nil
	}

	page := req.GetInt("page", 1)
	size := req.GetInt("size", 20)

	sc, err := config.RequireStore()
	if err != nil {
		return errResult("no store selected — use store_set first"), nil
	}

	client, err := s.apiClient(ctx, "")
	if err != nil {
		return errResult(fmt.Sprintf("failed to connect: %v", err)), nil
	}

	results, err := search.Search(ctx, client, sc.PdvRef, query, page, size)
	if err != nil {
		return errResult(fmt.Sprintf("product search failed: %v", err)), nil
	}

	var buf bytes.Buffer
	search.PrintResults(&buf, results, query, "json")
	return textResult(buf.String()), nil
}

func (s *state) handleBasketGet(ctx context.Context, _ mcp.CallToolRequest) (*mcp.CallToolResult, error) {
	client, customerID, err := s.authedClient(ctx)
	if err != nil {
		return errResult(err.Error()), nil
	}

	sc, err := config.RequireStore()
	if err != nil {
		return errResult("no store selected — use store_set first"), nil
	}

	cart, err := basket.GetCart(ctx, client, sc.PdvRef, customerID)
	if err != nil {
		return errResult(fmt.Sprintf("failed to get cart: %v", err)), nil
	}

	var buf bytes.Buffer
	basket.PrintCart(&buf, cart, "json")
	return textResult(buf.String()), nil
}

func (s *state) handleBasketAdd(ctx context.Context, req mcp.CallToolRequest) (*mcp.CallToolResult, error) {
	ean, err := req.RequireString("ean")
	if err != nil {
		return errResult("missing required parameter: ean"), nil
	}

	qty := req.GetInt("quantity", 1)

	client, customerID, err := s.authedClient(ctx)
	if err != nil {
		return errResult(err.Error()), nil
	}

	sc, err := config.RequireStore()
	if err != nil {
		return errResult("no store selected — use store_set first"), nil
	}

	cart, title, err := basket.AddItem(ctx, client, sc.PdvRef, customerID, ean, qty)
	if err != nil {
		return errResult(fmt.Sprintf("failed to add item: %v", err)), nil
	}

	return textResult(fmt.Sprintf("Added %d x %s to basket (%d distinct items in cart)", qty, title, cart.ItemsNumber)), nil
}

func (s *state) handleBasketRemove(ctx context.Context, req mcp.CallToolRequest) (*mcp.CallToolResult, error) {
	ean, err := req.RequireString("ean")
	if err != nil {
		return errResult("missing required parameter: ean"), nil
	}

	qty := req.GetInt("quantity", 0)

	client, customerID, err := s.authedClient(ctx)
	if err != nil {
		return errResult(err.Error()), nil
	}

	sc, err := config.RequireStore()
	if err != nil {
		return errResult("no store selected — use store_set first"), nil
	}

	cart, title, err := basket.RemoveItem(ctx, client, sc.PdvRef, customerID, ean, qty)
	if err != nil {
		return errResult(fmt.Sprintf("failed to remove item: %v", err)), nil
	}

	if qty == 0 {
		return textResult(fmt.Sprintf("Removed all %s from basket (%d distinct items in cart)", title, cart.ItemsNumber)), nil
	}
	return textResult(fmt.Sprintf("Removed %d x %s from basket (%d distinct items in cart)", qty, title, cart.ItemsNumber)), nil
}

func (s *state) handleOrdersList(ctx context.Context, _ mcp.CallToolRequest) (*mcp.CallToolResult, error) {
	client, customerID, err := s.authedClient(ctx)
	if err != nil {
		return errResult(err.Error()), nil
	}

	sc, err := config.RequireStore()
	if err != nil {
		return errResult("no store selected — use store_set first"), nil
	}

	list, err := orders.ListOrders(ctx, client, customerID, sc.PdvRef)
	if err != nil {
		return errResult(fmt.Sprintf("failed to list orders: %v", err)), nil
	}

	var buf bytes.Buffer
	orders.PrintOrders(&buf, list, "json")
	return textResult(buf.String()), nil
}

func (s *state) handleOrderDetail(ctx context.Context, req mcp.CallToolRequest) (*mcp.CallToolResult, error) {
	orderNumber, err := req.RequireString("order_number")
	if err != nil {
		return errResult("missing required parameter: order_number"), nil
	}

	client, customerID, err := s.authedClient(ctx)
	if err != nil {
		return errResult(err.Error()), nil
	}

	sc, err := config.RequireStore()
	if err != nil {
		return errResult("no store selected — use store_set first"), nil
	}

	detail, err := orders.GetOrder(ctx, client, customerID, sc.PdvRef, orderNumber)
	if err != nil {
		return errResult(fmt.Sprintf("failed to get order: %v", err)), nil
	}

	var buf bytes.Buffer
	orders.PrintOrderDetail(&buf, detail, "json")
	return textResult(buf.String()), nil
}
