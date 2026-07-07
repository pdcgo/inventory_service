# Restock Implementation

## Restock Update.
1. proto request schema obey this:
```

message RestockUpdateAction {
    oneof act {
        ChangeStatus change_status
        UpdateItems update_items
        UpdateShippingFee update_shipping_fee
        UpdateShippingInfo update_shipping_info
        RestockCancel restock_cancel
        RestockAccept restock_accept
    }
}

message RestockUpdateRequest {
    repeated RestockUpdateAction actions
}
```
2. action `change_status`, `update_items`, `update_shipping_fee`, `restock_cancel` and `update_shipping_info` only available when restock not accepted.

3. restock have field `inventory_transaction_id` that nullable.
    - if accepted, it set to inventory transaction that warehouse acceepted.
    - if it null, its indicated not accepted.

5. `RestockAccept` action accomodate:
    - goods placement
    - problem goods that happen when accept
    Example proto: 
    ```
    message RestockAccept {
        Placements placements
        Problem problems
        double warehouse_accept_fee
    }
    ```
    - ensure when accepting restock, count of problem + accepted in placement are equal with restocked item.
    - if `warehouse_accept_fee` not 0, team have payable to current warehouse who accepted.
    - rule pricing batch implementation when stock accepted is:
        ```
        price_item_batch = price_per_item_accepted + ((shipping_fee + warehouse_accept_fee) / piece_that_accepted)
        ```

