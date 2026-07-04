
# Inventory Service
This Is part of Submodule of [Warehouse Infra](https://github.com/pdcgo/warehouse_infra). In Warehouse Infra this is live in folder `./inventory_service`.<br>
This Service planned and Intended for replacing legacy Inventory System that exists in Warehouse Infra. Its planned for microservice and planned to more dependentless, separating domain purpose for better developing big and complex system that exists in Warehouse Infra.<br>
For now its just be candidate for Take over Inventory System In Warehouse Infra legacy.
Status for this development is still in progress and not completely take over legacy system.

1. for proto definition schema guideline read this [Proto Schema Guideline](proto-guideline.md).
2. for database schema related, read this [Database Schema](database-schema.md).
3. for Summary, Status, and Progress of Development read this [Development Summary](development-summary.md).


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

6. Placements RPC
7. Order RPC
8. Inbound RPC
9. Opname RPC

## Under Brainstorming and not final
1. how serve average performace status change


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
