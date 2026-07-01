
# Connect RPC Interface Of Inventory Service
`InventoryService` heavyly depend `connect-rpc` to serve and creating apis and grpc. Why we use `connectrpc` because its can be two mode as pure grpc and grpc-web that interact like web. And also supported http2

## Spec Must have
1. Transaction Related RPC
	- Create Transaction that named `TransactionCreate`
	- Cancel Transaction that named `TransactionCancel`
2. Product Related RPC
	- Product Config that named `ProductConfig`
	- Update Product Config that named `ProductConfigUpdate`
3. Rack Management RPC
	- rpc for creating rack that named `RackCreate`
	- rpc for updating rack that named `RackUpdate`
	- rpc for deleting the rack that named `RackDelete`
	- rpc for getting detail the rack that named `RackDetail`
	- rpc for getting rack list that named `RackList`

### Rule For Creating All Api List
All api list must be flexible for load various data and maybe various metric of statistic. So we need flexible structure that can cover it.

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

}

message TransactionRestock {

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





# Database Schema

## Model That Have
1. Product Config




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

