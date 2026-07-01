# resync batch

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
`
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
`
- adjust sql if needed

