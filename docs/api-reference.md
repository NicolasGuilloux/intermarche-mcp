# Intermarché API Reference

## Architecture

- **Frontend**: Next.js app with Redux (createAsyncThunk)
- **Proxy**: All backend calls go through `/api/service${url}` on `www.intermarche.com`
  - e.g. `/panier/v1/stores/...` is called as `/api/service/panier/v1/stores/...`
- **CDN**: `cdn.intermarche.com` for static assets and translations

## Common Headers (added by frontend middleware)

```
x-red-device: red_fo_desktop
x-red-version: 3
x-service-name: <first path segment of backend URL>
x-itm-user-agent: <browser UA>
X-ITM-SESSION-ID: <uuid>
X-ITM-DEVICE-FP: ghost_<session-uuid>
X-B3-TraceId / X-B3-SpanId / X-B3-Sampled (Zipkin tracing)
x-call-trace: web
x-is-server: false
x-oauth: true (for authenticated requests)
```

## Authentication

See [authentication.md](authentication.md) for the full OIDC/Keycloak flow.

- **Identity Provider**: Keycloak at `itmconnect.intermarche.com/auth/realms/customers`
- **client_id**: `desktop`
- **Flow**: Authorization Code + PKCE (S256), redirect to `/api/connexion`
- **Device Auth Grant** supported (ideal for CLI)
- `GET /api/auth/ping` - Check auth status
- `POST /api/auth/logout` - Logout
- `GET /api/connexion` - OAuth callback (exchanges code for tokens)
- Authenticated API calls use `x-oauth: true` header + `customerId` param

## Cart / Panier API

### Get/Update Cart
**POST** `/panier/v1/stores/${storeId}/carts`
- Params: `customerId` (authenticated) OR `anonymousCartId` (anonymous), `actions=VALUATION,ANIMATIONS`
- Headers: `x-oauth: true` (if authenticated)
- Body:
```json
{
  "customerDateTime": "2026-02-27T12:00:00+01:00",
  "events": [
    {
      "type": "QUANTITY",
      "itemId": "<productId>",
      "quantity": 1,
      "trackingCode": "...",
      "sponsorshipTag": "..."
    }
  ],
  "lastSynchronizedCart": "..."
}
```

### Cart sync model
This is a **sync-based cart**: the entire cart state is sent each time, with events describing changes.
- Event types: `QUANTITY`, `COMMENT`, `PRODUCT_SUBSTITUTION`, `CART_SUBSTITUTION`
- Items in cart: `{product, qty, sellerId}` - sellerId typically "ITM"

### Merge Carts
**POST** `/panier/v1/stores/${storeId}/merge`
- Params: `customerId`, `anonymousCartId`, `keepAuthenticatedCarts`

### Get Cart by ID
**GET/POST** `/panier/v1/stores/${storeId}/carts`
- Params: `customerId` or `anonymousCartId`, `isActiveAnonymousPersistence=true`

## Product Search

### Search by keyword
**POST** `/produits/v4/pdvs/${pdvRef}/products/byKeywordAndCategory`
- Headers: standard + `x-oauth: false` (works without auth)
- Body:
```json
{
  "keyword": "lait",
  "page": 1,
  "size": 20,
  "filtres": [],
  "tri": "pertinence",
  "ordreTri": "CROISSANT",
  "catalog": "courses-en-ligne"
}
```
- Response:
```json
{
  "searchResultsMetaData": {
    "resultNbre": 1074,
    "resultNbrePerPage": 20,
    "currentPage": 1,
    "totalPageNbre": 54,
    "categories": [{"categoryId": 2233, "label": "Laits et Boissons lactées", "productsCount": 132}]
  },
  "produits": [
    {
      "identifier": 127871,
      "produitEan13": "3533630097654",
      "libelle": "Grandlait frais - Lait frais de Montagne entier",
      "marque": "Grandlait",
      "prix": 1.79,
      "prixKg": 1.79,
      "unitePrixVente": {"value": "€/L"},
      "conditionnement": "la bouteille de 1 l",
      "stock": 0,
      "qteMaxPanier": 99,
      "images": ["https://assets-big.cdn-mousquetaires.com/..."],
      "tracking": {"code": "...", "type": "PREDIGGO"}
    }
  ],
  "filtres": [{"type": "...", "id": "...", "libelle": "...", "nbProduits": 10}],
  "suggestedKeywords": [],
  "isPresentAlcoholProduct": false
}
```

### Other product endpoints
- **POST** `/produits/v3/pdvs/${pdvRef}/products/suggestion` - Product suggestions
- **POST** `/produits/v3/pdvs/${pdvRef}/products/byProperty` - Find by internal IDs
- **POST** `/produits/v3/stores/${pdvRef}/products/byEans` - Find by EAN barcodes
- **POST** `/produits/v3/pdvs/${pdvRef}/products/retrieve` - Retrieve specific products
- **POST** `/produits/v1/stores/${pdvRef}/products/recommendations` - Recommendations
- **GET** `/produits/v2/consommateurs/${userId}/produits-favoris` - Frequent/favorite products
- **GET** `/produits/v3/pdvs/00000/produits/${ean}?extensions=...` - Single product details

## Product Model

```
ean          - EAN barcode (used as product identifier in cart events)
id           - internal product ID
prices.productPrice.value / .currency
prices.unitPrice.value
informations.brand
universId    - category
maxQty       - max purchasable quantity
stock        - stock level
trackingCode
sponsorShipTag
title
```

## Shopping Lists (Listes de courses)

- **GET** `/consommateur/v1/consommateurs/${userId}/listes_courses` → lists
- **POST** `/consommateur/v1/consommateurs/${userId}/listes_courses` → create `{nom}`
- **DELETE** `/consommateur/v1/consommateurs/${userId}/listes_courses/${listId}`
- **PATCH** `/consommateur/v1/consommateurs/${userId}/listes_courses/${listId}/produits?numero_pdv=${pdvRef}`
  - Add: `{produits: [{ean, privateData: '{"nfProductId":id}', quantity: 1}]}`
  - Remove: same with `quantity: 0`
- **POST** `/consommateur/v1/consommateurs/${userId}/listes_courses/${listId}?numero_pdv=${pdvRef}`
  - Get products: `{filtres: [], page, size, tri: "FAVORIS", ordreTri: "CROISSANT"}`

## Customer / Consumer

- **POST** `/consommateur/v1/consommateurs/${userId}/produits-favoris` - Get frequent products
- **DELETE** `/consommateur/v1/consommateurs/${userId}/produits-favoris` - Remove favorites
- **POST** `/consommateur/v1/consommateurs/${userId}/promotional-products` - Promotions
- **POST** `/consommateur/v2/customers/${userId}/transactions` - Order history
- **GET** `/consommateur/v1/customers/${userId}/transactions/${transId}` - Order details
- **GET** `/consommateur/v1/customers/${userId}/receipts/${receiptId}` - Receipt PDF

## Delivery Slots

- **POST** `/tunnelachat/v1/tunnels/${userId}/deliveries?activateSlotFilter=true`

## Other Endpoints

- **GET** `/api/feature-flips?pdvref=${pdvRef}` - Feature flags
- **GET** `/api/incidents` - Service incidents
- **GET** `/api/categories?...` - Product categories
- **GET** `/api/pdv-info?pdvRef=${ref}` - Store (PDV) information
- **GET** `/bff-cms/api/v1/avantages/web?pdvref=...&isecommerce=...` - Loyalty advantages

## Key Concepts

- **PDV** (Point de vente) = Store. Must be selected before browsing products.
- **pdvRef** = Store reference number (e.g. "00000" = no store selected)
- **EAN** = European Article Number, used as product identifier
- **sellerId** = "ITM" for Intermarché's own products
- **subCarts** = Cart can contain items from different sellers
