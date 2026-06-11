package inventory_models

// StockChangeType is the reason a stock/placement/batch quantity changed. The
// values mirror warehouse_iface.v1.StockChangeType so events map 1:1.
type StockChangeType int16

const (
	StockChangeUnspecified                  StockChangeType = 0
	StockChangeOrderAccepted                StockChangeType = 1
	StockChangeOrderCanceled                StockChangeType = 2
	StockChangeRestockAccepted              StockChangeType = 3
	StockChangeReturnAccepted               StockChangeType = 4
	StockChangeStockProblem                 StockChangeType = 5
	StockChangeStockFoundBack               StockChangeType = 6
	StockChangeStockAdjustment              StockChangeType = 7
	StockChangeTransferWarehouseOut         StockChangeType = 8
	StockChangeTransferWarehouseIn          StockChangeType = 9
	StockChangeTransferWarehouseOutCanceled StockChangeType = 10
	StockChangeTransferWarehouseInCanceled  StockChangeType = 11
)
