
# Inventory Service.
This Is part of Submodule of [Warehouse Infra](https://github.com/pdcgo/warehouse_infra). In Warehouse Infra this is live in folder `./inventory_service`.<br>
This Service planned and Intended for replacing legacy Inventory System that exists in Warehouse Infra. Its planned for microservice and planned to more dependentless, separating domain purpose for better developing big and complex system that exists in Warehouse Infra.<br>
For now its just be candidate for Take over Inventory System In Warehouse Infra legacy.
Status for this development is still in progress and not completely take over legacy system.

1. for proto definition schema guideline read this [Proto Schema Guideline](proto-guideline.md).
2. for database schema related, read this [Database Schema](database-schema.md).
3. for Summary, Status, and Progress of Development read this [Development Summary](development-summary.md).
4. for Feature Brief. [Read This](feature-brief.md)

## Under Brainstorming and not final
1. how serve average performace status change


## Authentication & Authorization.
1. Use v2 roling system. not legacy system. for complete reference read [this](../../user_service/docs/readme.md#authentication--authorization)
2. use interceptor that live in [here](../../user_service/access_interceptors/interceptor.go)
3. DON'T use legacy interface on [this](../../shared/interfaces/authorization_iface/authorization.go)


## Connect RPC Spec
`InventoryService` heavyly depend `connect-rpc` to serve and creating apis and grpc. Why we use `connectrpc` because its can be two mode as pure grpc and grpc-web that interact like web. And also supported http2

1. Transaction Related RPC
	- Create Transaction that named `TransactionCreate`
	- Cancel Transaction that named `TransactionCancel`
	

2. Product Related RPC.
	
	This rpc used for viewing, manage configuration that product exists/related to the warehouse. 
	- Product Config that named `ProductConfig`
	- Update Product Config that named `ProductConfigUpdate`
	- Product List that named `ProductList`
	- Product Detail that named `ProductDetail`

5. Rack Management RPC
	- rpc for creating rack that named `RackCreate`
	- rpc for updating rack that named `RackUpdate`
	- rpc for deleting the rack that named `RackDelete`
	- rpc for getting detail the rack that named `RackDetail`
	- rpc for getting rack list that named `RackList`
	- rpc for getting rack by ids that named `RackByIds`

6. Placements RPC
	- rpc for move placement that named `PlacementMove`


7. Order RPC

8. Restock Related RPC
	- rpc for creating restock that named `RestockCreate`
	- rpc for updating restock that named `RestockUpdate`.<br>
		- including change status, cancel and other.
	- rpc for getting detail restock that named `RestockDetail`
	- rpc for getting restock list that named `RestockList`
	- rpc for getting log history restock. This is usable for audit. this rpc named `RestockLogList`
	- for further implementation [Read This](./restock-implementation.md).

9. Opname RPC

10. Transfer Stock Between Warehouse RPC.
	- rpc for creating transfer that named `TransferCreate`
	- rpc for canceling transfer that named `TransferCancel`
	- rpc for accepting transfer that named `TransferAccept`
	- rpc for getting detail the Transfer that named `TransferDetail`
	- rpc for getting Transfer list that named `TransferList`


### RPC Placements
1. rpc `PlacementMove` can be multiple. for example:
```
message PlacementItem {
	uint64 product_id
	uint64 from_rack_id
	uint64 to_rack_id
}

message PlacementMove {
	uint64 warehouse_id
	repeated PlacementItem placements
	string note
}	
```


### RPC Create Transaction
create transaction is used for create mutation of stock in inventory service.
1. add rpc named `TransactionCreate`
2. transaction rpc cover 
	- create order
	- create restock
	- create return
	- create good found back
	- create good problem
3. spec request must have obeyed
```
message TransactionOrder {
	...other fields
}

message TransactionRestock {
	...other fields
}

message TransactionCreateRequest {
	uint64 team_id
	uint64 warehouse_id
	oneof tx {
		TransactionOrder 	order
		TransactionRestock 	restock
	}

	...optional if needed
}
```

### RPC Product Config
For now, this rpc handle configuration related:
1. how pricing queue in `StockBatch` to be ordered. its have several mode:
	- FIFO
	- LIFO
	- By Expiring Date

2. how goods pick placement in `StockPlacement`. with bigger/smaller quantity of product on the rack (`StockPlacement`).
3. if product have no configuration. its use default config with:
	- price ordering queue with FIFO
	- picking placement with smaller quantity on the rack.

### RPC Product Config Update
this rpc used for updating `ProductConfig` that getted in rpc `ProductConfig`

### Rpc Product List.
1. this rpc depend on database schema `Warehouse Product`
2. data that can be loaded is:
	- general info:
		- Product Name
		- Team Owner
		- images
	- stock state that on `StockState` in `inventory_models`:
		- Stock Ready
		- Stock Ready Amount


### RPC Rack Management
1. rpc `RackList` data that can be loaded is :	
	- General Info:
		- Rack Name
		- Rack Created
	- Stock Info
		- Stock Count
		- Product Count
	- Warehouse Info
		- warehouse name

2. rpc `RackList` have several filter:
	- warehouse_id
	- search by name
	- team_id
	- product_id

3. all rpc rack management except `RackList` is always scoping by warehouse_id

#### Rack Info for preloading other service RPC
1. All authenticated user can access `RackByIds`




### RPC Transfer Stock Between Warehouse
Transfer is a **two-legged, in-transit workflow document** (mirroring the legacy
`WarehouseTransfer` semantics): the stock leaves the source when the transfer is created and
enters the destination only when it is accepted.

1. lifecycle (`TransferStatus`): `PENDING` (in transit) -> `ACCEPTED` / `CANCELED`.
	- `TransferCreate` applies the **OUT leg** at the source immediately (an
	  `InventoryTransaction{transfer_out}`; stock exits `StockState`). Item prices are
	  **derived from the source `StockState` average** (`stock_ready_amount / stock_ready`),
	  so value is conserved — the request carries no price.
	- `TransferAccept` applies the **IN leg** at the destination
	  (`InventoryTransaction{transfer_in}`; stock enters + a `StockBatch` is minted at the
	  derived prices). Idempotent.
	- `TransferCancel` is **pre-accept only**: it reverses the OUT leg so the source gets its
	  stock back. An accepted transfer cannot be canceled — send a transfer in the opposite
	  direction instead. Idempotent.
2. `TransferList` follows the flexible list convention (`proto-guideline.md`): data types
	`GENERAL` (from/to warehouse + names, status, created/accepted) + `TOTAL`
	(item_count/amount), sort oneof (created | amount/item_count), filters `warehouse_id`
	(**matches either side**), from/to warehouse, team_id, status.
3. `TransferDetail` returns the header (with warehouse names + both leg transaction ids) and
	items with product names.

### resync batch

- planning trigger create stock batch also
- write synchornization stockbatch
- all event start use stockbatch



1. when:
	- StockEvent_RestockAccepted
	- StockEvent_ReturnAccepted
	- StockEvent_TransferWarehouseAccepted
	- StockEvent_StockFoundBack

2. create StockBatch.
	- the source is came of this
	```
	select
		ih.in_tx_id as batch_code,
		ih.warehouse_id,
		s.product_id,
		ih.count,
		(ih.price + coalesce(ih.ext_price, 0)) as price
	from invertory_histories ih
	left join skus s on s.id = ih.sku_id 
	where
		ih.in_tx_id = 1710872
		and ih.tx_id is not null
	```
	- adjust sql if needed

## Tracing & Debugging
