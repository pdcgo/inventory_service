# Summary Status And Progress of The Development

Running log of status, progress, and decisions. Update this after any inventory_service dev work
(see the rule in the repo root `CLAUDE.md`). Most recent first.

---

## 2026-07-03 — Transaction RPCs (TransactionCreate / TransactionCancel) — DONE

Implemented the request-driven, **inventory-owned** stock mutation RPCs (readme "Transaction Related RPC"),
one handler + shared test file.

- **Proto** — `TransactionCreate` / `TransactionCancel` + messages in [service.proto](../../schema/inventory_iface/v1/service.proto).
  `TransactionCreateRequest{ team_id, warehouse_id, oneof tx { order|restock|stock_return|found_back|problem } }`,
  each a `repeated TransactionItem{ product_id, count, price }`; returns `transaction_id`.
- **Model / migration** — new `inventory_models.InventoryTransaction` + `InventoryTransactionItem`
  (type + status) with goose `00008_create_inventory_transactions.sql`. Distinct from the legacy warehouse
  `inv_transactions` (per the "dependentless microservice" goal).
- **Behavior / decisions**
  - Applies to **StockState** via the existing `inventory_mutations.NewProcessStockBatchLog` (the five kinds map to
    the existing `StockChange` reasons; order/problem decrement, restock/return/found_back increment) and **mints a
    `StockBatch`** per product for inbound kinds (`mintTransactionBatches`, batch_code = tx id, ON CONFLICT DO NOTHING).
  - **Per-rack `StockPlacement` is intentionally skipped in v1** (request carries no racks).
  - `TransactionCancel` reuses `reconstructCancel` (sums the transaction's `StockBatchLog` and writes the reversing
    log; reversal reason = generic `Adjustment`), then voids the minted batch (`inbound_id`) and flips status to
    `canceled`. Idempotent (status guard + reconstructCancel's net-zero guard). Auth open (warehouse_id scoping).
- **Verified** — `make proto-gen` clean; `go build ./...` + `go vet ./...` clean; `TestTransaction` green (all five
  create kinds assert signed StockState + inbound batch; cancel reverses state/voids batch/marks canceled + idempotent
  + NotFound); full inventory package tests still green.

### Known / open
- **Reversal is logged as generic `Adjustment`** (audit-simple) — per-type "canceled" reasons can come later.
- **`TransactionCancel` relies on the `invertory_histories` (+ `skus`) tables existing** — reconstructCancel's placement
  step reads them (no-op for owned transactions since there are no matching rows). True in the target DB; tests migrate
  stand-ins.
- **No frontend/MCP** wiring yet (backend RPCs only).

---

## 2026-07-01 — Product Management RPCs — DONE

Implemented `ProductConfig / ProductConfigUpdate / ProductList / ProductDetail` (readme "Product Related RPC"),
one handler + one test file per RPC.

- **Proto** — 4 RPCs + messages in [service.proto](../../schema/inventory_iface/v1/service.proto); extended
  `QueueType` with `BY_EXPIRING_DATE` and added `PlacementPickingType` (SMALLER/BIGGER) in
  [types.proto](../../schema/inventory_iface/v1/types.proto). `ProductList` uses the flexible list convention
  (data types GENERAL/STOCK/TEAM/CONFIG + sort oneof + `map<uint64,Item>` + `ids`).
- **Model / migration** — new `inventory_models.ProductConfig` (per `(product_id, warehouse_id)`, unique) + goose
  `00007_create_product_configs.sql`.
- **Behavior / decisions** — `ProductConfig` per `(product, warehouse)`; get returns the stored row or the default
  (`FIFO` + `SMALLER`, `configured=false`); `ProductConfigUpdate` upserts via `clause.OnConflict`.
  `ProductList`/`Detail` sourced from `stock_states` (products tracked in the warehouse); aggregates = StockState
  stock_ready/amount + open `stock_batches` (end_count>0) + distinct `stock_placements` racks (count>0); name from
  `products`, team from `teams`. Auth open (warehouse_id scoping).
- **Verified** — `make proto-gen` clean; inventory_service + omnibus build clean; all 4 `TestProduct*` green plus the
  existing product/rack/reconcile tests. (Also fixed a `RackCreate` test assertion after the Rack model's ID/
  WarehouseID moved to `uint64`.)

### Known / open
- **"By expiring date" queue mode is stored but not enforced** — `StockBatch` has no expiry column and batch
  consumption/FIFO isn't built yet. Add a `StockBatch.expiring_at` + wire the batch-selection logic when
  consumption lands.
- Product/Rack RPCs aren't exposed as MCP tools and have no frontend client yet.

---

## 2026-07-01 — Rack Management RPCs — DONE

Implemented `RackCreate/RackUpdate/RackDelete/RackDetail/RackList` (per `docs/readme.md`), one handler file + one
test file per RPC (`inventory/rack_<op>.go` + `inventory/rack_<op>_test.go`).

- **Proto** — added the 5 RPCs + messages to [service.proto](../../schema/inventory_iface/v1/service.proto);
  `RackList` follows the flexible list convention (`proto-guideline.md`): `RackListDataType`
  (GENERAL/STOCK/WAREHOUSE) + per-type sort oneof + `repeated RackData` (`map<uint64,Item>`) + `ids`.
- **Model / migration** — `inventory_models.Rack` (duplicated from `db_models.Rack` per `database-schema.md`);
  goose `00006_create_racks.sql` = `CREATE TABLE IF NOT EXISTS racks` (legacy-compat, Down is a no-op so a
  pre-existing legacy `racks` is never dropped).
- **Behavior / decisions** — non-list RPCs scoped by `warehouse_id`; soft-delete via `deleted` flag
  (`deleted=false` filter everywhere); `RackDetail` returns stock count (`sum(count)`) + product count
  (`count(distinct product_id) FILTER (WHERE count>0)`) + warehouse name. **`team_id` filter** on `RackList` =
  "racks holding the team's stock" (`EXISTS stock_placements → products.team_id`), since the schema has no
  team↔warehouse ownership. **Auth** kept open (no access interceptor; warehouse_id scoping only).
- **Verified** — `make proto-gen` clean; inventory_service + omnibus build clean; all 5 `TestRack*` green
  (create/update/delete/detail/list incl. search, team filter, both sorts, GENERAL/STOCK/WAREHOUSE maps, soft-delete
  exclusion). Also corrected `TestProductReconcileRPC`'s streamed-line count (5→7: 1 state + 3 placement + 2
  per-batch + 1 batch-success) to match the batch-sync handler.

**Update:** added the **`product_id`** filter to `RackList` (readme's filter list gained it) — filters to racks
currently holding the product (`EXISTS stock_placements WHERE product_id = ? AND count > 0`), composing with the
other filters; covered by a `rack_list_test.go` subtest.

### Next / open
- Rack RPCs are not exposed as MCP tools and have no frontend client yet.
- `team_id` filter semantics can be revisited if a real team↔warehouse ownership is introduced.

---

## 2026-07-01 — StockBatch creation + ProductReconcile streaming

### StockBatch creation on stock-entering events — DONE
- `ProcessStockEvent` now mints a `StockBatch` per (product, warehouse) whenever stock enters, gated in
  `applyTxItems` by `isInboundChange` — the four inbound change types: `RESTOCK_ACCEPTED`, `RETURN_ACCEPTED`,
  `STOCK_FOUND_BACK`, `TRANSFER_WAREHOUSE_IN` (transfers use `WarehouseTransfer.InboundTxID`).
- `createStockBatches(tx, inboundTxID, now)` aggregates `invertory_histories` (join `skus`) filtered
  `in_tx_id = ? AND tx_id = in_tx_id` (inbound placement rows only, so already-shipped stock isn't netted out),
  grouped by (warehouse, product): `StartCount = EndCount = sum(count)`, count-weighted landed `Price`
  (`price + ext_price`), `BatchCode = in_tx_id`. Idempotent via a pre-check on `inbound_id`.
- Files: [inventory/process_stock_event.go](../inventory/process_stock_event.go),
  test [inventory/process_stock_event_batch_test.go](../inventory/process_stock_event_batch_test.go).
- Verified: `go test ./inventory/... . -p 1` green (needs local Postgres); omnibus builds.

### ProductReconcile → server-streaming — DONE
- Proto: `rpc ProductReconcile(...) returns (stream ProductReconcileResponse)` and
  `ProductReconcileResponse { string message = 1; }` (regenerated via `make proto-gen`).
- Handler streams `slog` progress lines to the client through `ReconcileLogger` (an `io.Writer` wrapping the
  server stream). It reconciles StockState + per-rack StockPlacement to the legacy on-hand snapshot
  (`invertory_histories` where `tx_id IS NULL`), then in a second transaction syncs `StockBatch` from that legacy
  on-hand (`batch_code = coalesce(in_tx_id::text, 'legacy-<sku>-<price>')`, `OnConflict DoNothing`).
- `cmd/app_production/sync_legacy.go` drains the server stream and logs each `message`.
- Test uses an in-process Connect httptest server + real streaming client (connect-go's `ServerStream` has no
  exported constructor), mirroring `user_service/user/team_sync_legacy_test.go`.
- Files: [inventory/product_reconcile.go](../inventory/product_reconcile.go),
  test [inventory/product_reconcile_test.go](../inventory/product_reconcile_test.go),
  [cmd/app_production/sync_legacy.go](../cmd/app_production/sync_legacy.go).
- Verified: `TestProductReconcileRPC` green; inventory_service + omnibus build clean.

### Known / pending
- **Two StockBatch-creation paths key `batch_code` differently** — event path uses `in_tx_id`; the reconcile path
  uses `coalesce(in_tx_id, 'legacy-<sku>-<price>')`. No invariant enforces `sum(StockBatch.EndCount) ==
  StockState.StockReady`; worth an invariant + test.
- **StockBatch is write-only** — nothing consumes batches yet (`EndCount` never decremented; the
  `WHERE end_count > 0` open-batches index is unused). FIFO/`ProductConfig` picking is still TODO.
- **Pre-existing, unrelated:** `TestSyncLegacy` (cmd/app_production) panics on a nil `*cli.Command` in its own
  setup — not caused by the above; needs a non-nil `cli.Command`/flag set wired into the test.

### Next
- **Rack Management** (`RackCreate/Update/Delete/Detail/List`) — planning underway; RackList to follow the
  `proto-guideline.md` flexible list pattern. Open design questions: the `team_id` filter semantics (no
  team↔warehouse ownership in the schema), RackList shape (flexible vs simple), and auth scoping.
