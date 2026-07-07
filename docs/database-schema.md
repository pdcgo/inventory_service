# Database Schema & Model

## Migration Rule
1. its HARD RULE, ALWAYS CONFIRM DEVELOPER before create new migration or alter schema. 


## Schema Requirements

Schema and Model that have :
1. Product Config
2. Rack
3. Warehouse Product
4. Inventory Transaction
5. Inventory Restock
6. Warehouse


## Inventory Restock Schema.
1. must have field:
    - status:
        - pending
        - completed
        - problem
        - problem_completed
        - cancel
    - shipping info.<br> 
        Because we restock from ordering other shop.
        - order id (optional string)
        - receipt (optional string)
        - payment method:
            - bank 
            - shopee pay

    - accepted_transaction_id

2. Restock can be broken, so wee need schema `RestockItemProblem`.
    - must have field problem_type:
        - broken
        - lost

3. we must have `RestockLog` for the audit.<br>
    - must have field user_id, that used by who doing the action.
    - must have field action_type. action_type is:
        - change status
        - edit item
        - edit shipping_fee
        - edit shipping info
        - cancel
  




## Rack Schema
1. Legacy compatibility
    Because Rack Schema is exist in legacy system before. we must aware about the migration. in new migration this project, create rack **If Only** that table is not exist.
2. This is legacy golang struct that reflected the schema. for now, the field and schema already accomodate this system. No need to change.
    ```
    type Rack struct {
        ID          uint   `json:"id" gorm:"primarykey"`
        WarehouseID uint   `json:"warehouse_id"`
        Name        string `json:"name"`
        IsSystem  bool      `json:"is_system"`
        CreatedAt time.Time `json:"created_at" gorm:"autoCreateTime:milli"`
        Deleted   bool      `json:"deleted" gorm:"index"`
    }
    ```
3. if on `./inventory_models` doesn't have golang model for that rack struct definition, duplicate legacy and place at `./inventory_models/placement.go`

## Warehouse Product Schema
1. Legacy compatibility
    Because Warehouse Product Schema is exist in legacy system before. we must aware about the migration. in new migration this project, create rack **If Only** that table is not exist.
2. This is legacy golang struct that reflected the schema. for now, the field and schema already accomodate this system. No need to change.
    ```
    type WarehouseProduct struct {
        ID          uint `json:"id" gorm:"primarykey"`
        WarehouseID uint `json:"warehouse_id" gorm:"index:ware_product,unique"`
        ProductID   uint `json:"product_id" gorm:"index:ware_product,unique"`
        Stock       uint `json:"stock"`                                         <--- but in new, dont include this

        Product   *Product   `json:"product"`                                   <--- but in new, dont include this
        Warehouse *Warehouse `json:"warehouse"`                                 <--- but in new, dont include this
    }

    ```
3. if on `./inventory_models` doesn't have golang model for that rack struct definition, duplicate legacy and place at `./inventory_models/product.go`

## Inventory Transaction Schema
1. its hold all transaction inventory that happen

## Inventory Transfer Schema
1. `inventory_transfers` + `inventory_transfer_items` (goose `00010_create_inventory_transfers.sql`,
   models in `inventory_models/transfer.go`) — the warehouse-to-warehouse transfer **workflow
   document**: team, from/to warehouse, note, status (`pending`/`accepted`/`canceled`),
   `out_transaction_id` (the source OUT leg, set at create) + `in_transaction_id` (the destination
   IN leg, 0 until accepted), created_by, timestamps.
2. items hold `(transfer_id, product_id, count, price)` where `price` is **derived from the source
   StockState average at create time** (value conserved). Stock mutates through the two linked
   `inventory_transactions` legs (`transfer_out` / `transfer_in`), never from the document rows.

## Inventory Restock Schema
1. `inventory_restocks` + `inventory_restock_items` (goose `00009_create_inventory_restocks.sql`,
   models in `inventory_models/restock.go`) — the restock **workflow document**: team/warehouse,
   supplier, receipt, note, status (`pending`/`accepted`/`problem`/`canceled`), `transaction_id`
   (0 until accepted; then links the minted `inventory_transactions` row), created_by, timestamps.
2. items hold `(restock_id, product_id, count, price)`; stock only mutates on accept (via the
   linked inventory transaction), never from the document rows themselves.
3. purchase/order info columns (goose `00011_add_restock_order_fields.sql`): `extern_order_id`,
   `shipping_id` (courier, common shipment id), `shipping_cost` (ongkir), `payment_type`
   (`'' | 'shopeepay' | 'transfer'`) — set by the selling-team create-restock flow.
4. audit + per-item problems (goose `00012_add_restock_logs_and_item_problems.sql`):
   `inventory_restock_logs` (restock_id, action `created|edited|accepted|problem|canceled`, note,
   created_by_id, created_at — one row per lifecycle event, written in the event's transaction;
   read by `RestockLogList`) and `problem_count`/`problem_note` columns on
   `inventory_restock_items` (set via `RestockUpdate{problem}.items`).
