package main

import (
	"context"
	"fmt"
	"net/http"
	"os"
	"os/signal"
	"strings"
	"syscall"
	"time"

	"strconv"

	mcpserver_lib "github.com/mark3labs/mcp-go/server"

	"github.com/nover/intermarche-mcp/internal/api"
	"github.com/nover/intermarche-mcp/internal/auth"
	"github.com/nover/intermarche-mcp/internal/basket"
	"github.com/nover/intermarche-mcp/internal/config"
	"github.com/nover/intermarche-mcp/internal/mcpserver"
	"github.com/nover/intermarche-mcp/internal/orders"
	"github.com/nover/intermarche-mcp/internal/search"
	"github.com/nover/intermarche-mcp/internal/solver"
	"github.com/nover/intermarche-mcp/internal/store"
)

func main() {
	ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt)
	defer stop()

	format := parseFormat()

	if len(os.Args) < 2 {
		printUsage()
		os.Exit(0)
	}

	var err error
	switch os.Args[1] {
	case "login":
		err = auth.Login(ctx)
	case "login-password":
		if len(os.Args) < 4 {
			fmt.Fprintf(os.Stderr, "Usage: intermarche-mcp login-password <email> <password>\n")
			os.Exit(1)
		}
		err = auth.LoginPassword(ctx, os.Args[2], os.Args[3])
	case "logout":
		err = auth.Logout()
	case "store":
		err = cmdStore(ctx, format)
	case "orders":
		err = cmdOrders(ctx, format)
	case "search":
		err = cmdSearch(ctx, format)
	case "basket":
		err = cmdBasket(ctx, format)
	case "mcp":
		err = cmdMCP(ctx)
	default:
		fmt.Fprintf(os.Stderr, "Unknown command: %s\n", os.Args[1])
		os.Exit(1)
	}

	if err != nil {
		fmt.Fprintf(os.Stderr, "Error: %v\n", err)
		os.Exit(1)
	}
}

// parseFormat extracts --format=<value> or --format <value> from os.Args,
// removes those entries from os.Args, and returns the format value ("table" by default).
func parseFormat() string {
	format := "table"
	filtered := os.Args[:0]
	for i := 0; i < len(os.Args); i++ {
		arg := os.Args[i]
		if strings.HasPrefix(arg, "--format=") {
			format = strings.TrimPrefix(arg, "--format=")
		} else if arg == "--format" && i+1 < len(os.Args) {
			i++
			format = os.Args[i]
		} else {
			filtered = append(filtered, arg)
		}
	}
	os.Args = filtered
	return format
}

func printUsage() {
	fmt.Println("intermarche-mcp - Intermarché store CLI")
	fmt.Println()
	fmt.Println("Usage: intermarche-mcp <command>")
	fmt.Println()
	fmt.Println("Commands:")
	fmt.Println("  login                       Log in with your Intermarché account")
	fmt.Println("  logout                      Log out and clear stored tokens")
	fmt.Println("  store set <ref>             Select your Intermarché store (PDV reference)")
	fmt.Println("  store search <query>        Search stores by city or postal code")
	fmt.Println("  orders                      List previous orders")
	fmt.Println("  orders <number>             Show order detail with articles")
	fmt.Println("  search <query>              Search for products")
	fmt.Println("  basket get                  Show current basket contents")
	fmt.Println("  basket add <ean> [qty]      Add a product to your basket (default qty: 1)")
	fmt.Println("  basket remove <ean> [qty]   Remove a product (default: remove all)")
	fmt.Println("  mcp                         Start MCP server (stdio transport)")
	fmt.Println("  mcp http [addr]             Start MCP server (Streamable HTTP, default :8080)")
}

// apiTransport is what every command needs from a transport: an http
// round-tripper that can be closed when done.
type apiTransport interface {
	http.RoundTripper
	Close() error
}

// dialTransport builds the API transport. Every request is wrapped through the
// Salamoonder solver (internal/solver), which clears Datadome over the network
// and caches the cleared cookie. It is the only transport.
func dialTransport(_ context.Context) (apiTransport, error) {
	return solver.New()
}

func cmdMCP(ctx context.Context) error {
	srv := mcpserver.NewServer()

	// mcp http [addr] → Streamable HTTP transport
	if len(os.Args) >= 3 && os.Args[2] == "http" {
		addr := ":8080"
		if len(os.Args) >= 4 {
			addr = os.Args[3]
		}
		httpSrv := mcpserver_lib.NewStreamableHTTPServer(srv)
		fmt.Printf("MCP Streamable HTTP server listening on %s\n", addr)

		// Register signal handler directly here — do not rely on the inherited
		// ctx from main(), which may be interfered with by other signal handlers
		// in the dependency tree. Buffer 2 so a rapid double-press is not lost.
		sigCh := make(chan os.Signal, 2)
		signal.Notify(sigCh, os.Interrupt, syscall.SIGTERM)
		defer signal.Stop(sigCh)

		errCh := make(chan error, 1)
		go func() { errCh <- httpSrv.Start(addr) }()

		select {
		case err := <-errCh:
			return err
		case <-sigCh:
			// first signal consumed; second will be a fresh send to sigCh
		}

		fmt.Fprintln(os.Stderr, "Shutting down (press Ctrl+C again to force)...")
		// Second signal: force-exit immediately.
		go func() {
			select {
			case <-sigCh:
				os.Exit(1)
			case <-time.After(6 * time.Second):
			}
		}()
		shutdownCtx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
		defer cancel()
		_ = httpSrv.Shutdown(shutdownCtx)
		return nil
	}

	// default: stdio transport
	return mcpserver_lib.ServeStdio(srv)
}

func cmdStore(ctx context.Context, format string) error {
	if len(os.Args) < 3 {
		return fmt.Errorf("usage: intermarche-mcp store <set|search> ...")
	}

	switch os.Args[2] {
	case "set":
		return cmdStoreSet()
	case "search":
		return cmdStoreSearch(ctx, format)
	default:
		return fmt.Errorf("unknown store command: %s", os.Args[2])
	}
}

func cmdStoreSet() error {
	if len(os.Args) < 4 {
		return fmt.Errorf("usage: intermarche-mcp store set <pdvRef>")
	}
	pdvRef := os.Args[3]

	if err := config.SaveStore(&config.StoreConfig{PdvRef: pdvRef}); err != nil {
		return fmt.Errorf("saving store: %w", err)
	}

	fmt.Printf("Store set to %s\n", pdvRef)
	return nil
}

func cmdStoreSearch(ctx context.Context, format string) error {
	if len(os.Args) < 4 {
		return fmt.Errorf("usage: intermarche-mcp store search <city or postal code>")
	}
	query := strings.Join(os.Args[3:], " ")

	transport, err := dialTransport(ctx)
	if err != nil {
		return err
	}
	defer transport.Close()

	client := api.New("", transport)

	results, err := store.Search(ctx, client, query)
	if err != nil {
		return err
	}

	store.PrintResults(os.Stdout, results, query, format)
	return nil
}

func cmdOrders(ctx context.Context, format string) error {
	token, err := auth.GetToken(ctx)
	if err != nil {
		return err
	}

	customerID, err := auth.UserID(token)
	if err != nil {
		return fmt.Errorf("reading user ID from token: %w", err)
	}

	store, err := config.RequireStore()
	if err != nil {
		return err
	}

	transport, err := dialTransport(ctx)
	if err != nil {
		return err
	}
	defer transport.Close()

	client := api.New(token, transport)
	client.RefreshFunc = func() (string, error) { return auth.ForceRefresh(ctx) }

	// orders <orderNumber> → show detail
	if len(os.Args) >= 3 {
		orderNumber := os.Args[2]
		detail, err := orders.GetOrder(ctx, client, customerID, store.PdvRef, orderNumber)
		if err != nil {
			return err
		}
		orders.PrintOrderDetail(os.Stdout, detail, format)
		return nil
	}

	// orders → list all
	list, err := orders.ListOrders(ctx, client, customerID, store.PdvRef)
	if err != nil {
		return err
	}
	orders.PrintOrders(os.Stdout, list, format)
	return nil
}

func cmdSearch(ctx context.Context, format string) error {
	if len(os.Args) < 3 {
		return fmt.Errorf("usage: intermarche-mcp search <query>")
	}
	query := strings.Join(os.Args[2:], " ")

	store, err := config.RequireStore()
	if err != nil {
		return err
	}

	transport, err := dialTransport(ctx)
	if err != nil {
		return err
	}
	defer transport.Close()

	client := api.New("", transport)

	results, err := search.Search(ctx, client, store.PdvRef, query, 1, 20)
	if err != nil {
		return err
	}

	search.PrintResults(os.Stdout, results, query, format)
	return nil
}

func cmdBasket(ctx context.Context, format string) error {
	if len(os.Args) < 3 {
		return fmt.Errorf("usage: intermarche-mcp basket <get|add|remove> ...")
	}

	switch os.Args[2] {
	case "get":
		return cmdBasketGet(ctx, format)
	case "add":
		return cmdBasketAdd(ctx)
	case "remove":
		return cmdBasketRemove(ctx)
	default:
		return fmt.Errorf("unknown basket command: %s", os.Args[2])
	}
}

func cmdBasketGet(ctx context.Context, format string) error {
	token, err := auth.GetToken(ctx)
	if err != nil {
		return err
	}

	customerID, err := auth.UserID(token)
	if err != nil {
		return fmt.Errorf("reading user ID from token: %w", err)
	}

	store, err := config.RequireStore()
	if err != nil {
		return err
	}

	transport, err := dialTransport(ctx)
	if err != nil {
		return err
	}
	defer transport.Close()

	client := api.New(token, transport)
	client.RefreshFunc = func() (string, error) { return auth.ForceRefresh(ctx) }

	cart, err := basket.GetCart(ctx, client, store.PdvRef, customerID)
	if err != nil {
		return err
	}

	basket.PrintCart(os.Stdout, cart, format)
	return nil
}

func cmdBasketAdd(ctx context.Context) error {
	if len(os.Args) < 4 {
		return fmt.Errorf("usage: intermarche-mcp basket add <ean> [qty]")
	}
	ean := os.Args[3]

	qty := 1
	if len(os.Args) >= 5 {
		n, err := strconv.Atoi(os.Args[4])
		if err != nil || n == 0 {
			return fmt.Errorf("invalid quantity: %s", os.Args[4])
		}
		qty = n
	}

	token, err := auth.GetToken(ctx)
	if err != nil {
		return err
	}

	customerID, err := auth.UserID(token)
	if err != nil {
		return fmt.Errorf("reading user ID from token: %w", err)
	}

	store, err := config.RequireStore()
	if err != nil {
		return err
	}

	transport, err := dialTransport(ctx)
	if err != nil {
		return err
	}
	defer transport.Close()

	client := api.New(token, transport)
	client.RefreshFunc = func() (string, error) { return auth.ForceRefresh(ctx) }

	cart, title, err := basket.AddItem(ctx, client, store.PdvRef, customerID, ean, qty)
	if err != nil {
		return err
	}

	fmt.Printf("Added %d x %s to basket (%d distinct items in cart)\n", qty, title, cart.ItemsNumber)
	return nil
}

func cmdBasketRemove(ctx context.Context) error {
	if len(os.Args) < 4 {
		return fmt.Errorf("usage: intermarche-mcp basket remove <ean> [qty]")
	}
	ean := os.Args[3]

	qty := 0 // 0 means remove all
	if len(os.Args) >= 5 {
		n, err := strconv.Atoi(os.Args[4])
		if err != nil || n <= 0 {
			return fmt.Errorf("invalid quantity: %s", os.Args[4])
		}
		qty = n
	}

	token, err := auth.GetToken(ctx)
	if err != nil {
		return err
	}

	customerID, err := auth.UserID(token)
	if err != nil {
		return fmt.Errorf("reading user ID from token: %w", err)
	}

	store, err := config.RequireStore()
	if err != nil {
		return err
	}

	transport, err := dialTransport(ctx)
	if err != nil {
		return err
	}
	defer transport.Close()

	client := api.New(token, transport)
	client.RefreshFunc = func() (string, error) { return auth.ForceRefresh(ctx) }

	cart, title, err := basket.RemoveItem(ctx, client, store.PdvRef, customerID, ean, qty)
	if err != nil {
		return err
	}

	if qty == 0 {
		fmt.Printf("Removed all %s from basket (%d distinct items in cart)\n", title, cart.ItemsNumber)
	} else {
		fmt.Printf("Removed %d x %s from basket (%d distinct items in cart)\n", qty, title, cart.ItemsNumber)
	}
	return nil
}
