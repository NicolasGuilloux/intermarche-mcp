# Add to Basket Flow

## Overview

Adding a product to the basket on Intermarché uses a **sync-based cart model**. Rather than
a simple "add item" endpoint, the frontend sends the entire cart state along with events
describing what changed.

## Prerequisites

1. **Store selected** - A PDV (point de vente) must be selected. The store ID (`pdvRef`)
   is part of the API URL path.
2. **Session** - Either authenticated (`customerId` + OAuth) or anonymous (`anonymousCartId`).

## Step-by-step

### 1. User clicks "Add to cart"

The frontend React component dispatches a Redux action:
```
dispatch(cart/updateCartQuantity({
  items: [{product, quantity: newQty, sellerId: "ITM"}],
  allowValorisation: true
}))
```

### 2. Event is created

The thunk builds a QUANTITY event:
```json
{
  "type": "QUANTITY",
  "itemId": "<product.ean>",
  "quantity": <delta>,
  "trackingCode": "<product.trackingCode>",
  "sponsorshipTag": "<product.sponsorShipTag>",
  "isRecipeProduct": false
}
```

The `quantity` is the **delta** (change), not absolute. Adding one item = `+1`.

### 3. Cart state is normalized and sent

The full cart state is "normalized" (serialized) and POSTed:

```
POST /api/service/panier/v1/stores/{storeId}/carts
    ?customerId={userId}           (if authenticated)
    ?anonymousCartId={anonId}      (if anonymous)
    &actions=VALUATION,ANIMATIONS  (if valorisation requested)
```

Headers:
```
x-oauth: true                     (if authenticated)
x-red-device: red_fo_desktop
x-red-version: 3
x-service-name: panier
X-ITM-SESSION-ID: <uuid>
Content-Type: application/json
```

Body (simplified):
```json
{
  "customerDateTime": "2026-02-27T14:30:00+01:00",
  "events": [
    {
      "type": "QUANTITY",
      "itemId": "3274080005003",
      "quantity": 1
    }
  ],
  "lastSynchronizedCart": "<timestamp or hash from previous sync>"
}
```

### 4. Response

The API returns the full updated cart state, which the frontend "denormalizes" back into
the Redux store. The response includes:

- Updated items with quantities, prices, promotions
- `basketId` - Cart identifier
- `subCarts` - Array of sub-carts grouped by seller
- `subCarts[].items` - Map of `productId -> {product, qty, total}`
- `subCarts[].total` - Sub-cart total
- `subCarts[].valorisation` - Applied discounts
- `synchronizedAt` - Timestamp of last sync

### 5. Analytics

After a successful cart update, tracking events are sent:
- `addToCart` event to analytics (GTM/dataLayer)
- LuckyCart cart sync (if enabled via feature flag)
- Relevanc tracking: `window.relevanc.addToCart({currency, price, product_id: ean, quantity, user_id, auction_id})`

## Removing from cart

Same flow but with negative quantity delta (`-1`). When quantity reaches 0, the item is
removed.

## Re-ordering a previous order

The "Repasser ma commande" button adds all products from a previous order:
```
items = previousOrder.products.map(p => ({product: p, quantity: p.qty, sellerId: "ITM"}))
dispatch(cart/updateCartQuantity({items, allowValorisation: true}))
```

## Cart merge (login while having anonymous cart)

When a user logs in and has items in an anonymous cart:
```
POST /api/service/panier/v1/stores/{storeId}/merge
    ?customerId={userId}
    &anonymousCartId={anonCartId}
    &keepAuthenticatedCarts=true
```

## Error handling

On cart update failure, the frontend redirects to `/commandes/panier` (the cart page).
