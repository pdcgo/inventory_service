# Summary Status And Progress of The Development

Running log of status, progress, and decisions. Update this after any inventory_service dev work
(see the rule in the repo root `CLAUDE.md`). Most recent first.

---

## 2026-08-12 — TransactionByIds gains type + status — DONE

`TransactionDetail` carried only `id`, `extern_order_id`, `receipt`. Callers resolving a
transaction id (notably the invoice ledger's `balance_change_broken_sources.tx_id` and the new
`recovery_tx_id`) could not tell a restock from an adjustment-in, nor whether it was completed
or cancelled.

- **Proto** — two new enums in `inventory_iface/v1`, plus `type = 4` / `status = 5` on
  `TransactionDetail`:
  - `TransactionType` — 14 values mirroring `db_models.InvTxType` (`restock, adj_restock, order,
    return, transfer_in, transfer_out, transit, broken, ch_sku_out, ch_sku_in, adj_in, adj_out,
    sys_err_in, sys_err_out`).
  - `TransactionStatus` — 8 values mirroring `db_models.InvTxStatus` (`waiting, ongoing, cancel,
    completed, picking, picked, packing, packing_completed`).
- **[inventory/transaction_by_ids.go](../inventory/transaction_by_ids.go)** — `transactionTypes`
  and `transactionStatuses` lookups, same shape as the existing `problemTypes` map; the `SELECT`
  gained `type` and `status`.

Decisions:
- Followed the `ProblemType` precedent: the columns stay free-form text in the DB, so an
  unrecognised value degrades to `UNSPECIFIED` rather than failing the call (covered by a test
  seeding `who_knows`).
- Included `sys_err_in` / `sys_err_out` even though `InvTxType.EnumList()` omits them — the
  constants exist and can be stored, and a gap would silently read as `UNSPECIFIED`.
- Test stand-in `invTxRow` gained `Type` and `Status` columns.

Verified: `make proto-gen`, `go build`, `go vet ./...`, `go test -count=1 -p 1 ./...` — green
except the pre-existing `TestSyncLegacy/sync_legacy_stock` nil-pointer panic in
`cmd/app_production` (same failure already recorded on 2026-07-16 below; untouched by this work).

---

## 2026-07-18 — TransactionProblemItemByTxItemIds — DONE

Fourth batch load-by-ids RPC, sibling of `TransactionItemByIds` but over a different table and
keyed differently.

- **Proto** — `TransactionProblemItemByTxItemIds`: request `repeated tx_item_ids` (cap 100),
  response `map<uint64, TransactionProblemItemDetail{id, tx_item_id, sku_id, problem_type,
  problem_note, count}>` **keyed by tx_item_id** (not by the problem row's own id).
- **[inventory/transaction_problem_item_by_tx_item_ids.go](../inventory/transaction_problem_item_by_tx_item_ids.go)**
  — reads `inv_item_problems` by **raw table name** with a local scan struct, matching how
  `process_stock_event.go` already joins that table. `inv_item_problems` is owned by
  warehouse_service, and inventory_service has no dependency on that module — adding one just for a
  model would be the wrong trade.

- `problem_type` is a typed enum, not a raw string: `ProblemType` mirrors the stored
  `ware_db.ProblemType` values (`broken_s, lost_s, diff_s, broken_w, lost_w, disaster, sample`),
  mapped in the handler via a `problemTypes` lookup.

Decisions:
- One tx item may carry several problem rows (different problem types). Owner chose a flat
  `map<uint64, Detail>` rather than a list, so rows are read `ORDER BY id ASC` and the **highest id
  wins deterministically** (documented in the proto and covered by a test).
- Column is `problem_type` (the model's `json:"broken_type"` tag is JSON-only) — verified against
  the existing `invoice_service` and `selling_service` queries.
- Enum values come from `ware_db.ProblemType` (7 canonical constants), **not** the 11-value
  `WarehouseProblemType` in `problem_product_list_warehouse.go` — those extra four (`broken_r`,
  `lost_r`, `broken`, `lost`) appear only in that legacy view as filter categories, with no evidence
  they are ever written to the column. The column stays free-form text, so an unrecognised value
  degrades to `PROBLEM_TYPE_UNSPECIFIED` instead of erroring (covered by a test seeding `broken_r`).
- Test stand-in `invItemProblem` in push_stock_event_test.go gained the columns this RPC reads.

Verified: `make proto-gen`, `go build`, `go vet ./inventory/`, full `go test ./inventory/` — all pass.

---

## 2026-07-18 — 3 batch load-by-ids RPCs (transaction / transaction item / product by sku) — DONE

Three "load Data By IDs" RPCs added to `InventoryService` (docs/proto-guideline.md pattern, à la
`RackByIds`), for preloading detail in other UIs. Named without a `Get` prefix to match `RackByIds`.

- **[schema/inventory_iface/v1/service.proto](../../schema/inventory_iface/v1/service.proto)** —
  three RPCs, each `option request_policy = {allow_only_authenticated: true}`, request `repeated
  ids` (cap 100), response a map keyed by id:
  - `TransactionByIds` → `map<uint64, TransactionDetail{id, extern_order_id, receipt}>`. Fields
    read directly off `InvTransaction` (`ExternOrdID` col `extern_ord_id`; `Receipt`); non-deleted only.
  - `TransactionItemByIds` → `map<uint64, TransactionItemDetail{id, inv_transaction_id, sku_id,
    count, price, total}>`. All fields on `InvTxItem`.
  - `ProductBySkuIds` → `map<string, ProductBySkuDetail{id, name, ref_id, image}>` **keyed by
    string sku_id** (SkuID is an encoded string, not uint).
- **Handlers** ([inventory/get_transaction_by_ids.go](../inventory/get_transaction_by_ids.go),
  [get_transaction_item_by_ids.go](../inventory/get_transaction_item_by_ids.go),
  [get_product_by_sku_ids.go](../inventory/get_product_by_sku_ids.go)) — implemented
  (`WHERE id IN ?` → map). No manual registration. Filenames keep the `get_` prefix; the RPC/method
  names dropped it.

Design decisions (owner-confirmed):
- `sku_id` is a string → request `repeated string`, response `map<string, ...>`. `ProductBySkuIds`
  decodes each sku_id via `SkuID.Extract().ProductID` (no `skus` table hit), batch-loads the
  products, then keys the result back by sku_id. Undecodable / missing-product sku ids are omitted.
- `Product.Image` is a JSON string array → RPC returns the **first** element (empty when none).
- `Product.RefID` returned as the **raw** string (no `.ExtractData()`).
- Auth is proto-declared only (no handler auth call), matching `RackByIds`; inert until the v2
  interceptor is mounted.

Tests: [inventory/get_by_ids_test.go](../inventory/get_by_ids_test.go) — one per handler (moretest,
minimal `TableName()` stand-ins to dodge the association cascade). `ProductBySkuIds` builds real sku
ids via `NewSkuID` so the `Extract()` decode path is exercised, and asserts deleted / missing-product
/ undecodable sku ids are omitted and `image` is the first array element.

Verified: `make proto-gen`, schema + inventory_service `go build`, `go vet ./inventory/`, and the full
`go test ./inventory/` — all pass.

---

## 2026-07-08 — TransactionCreate ORDER kind: avg-cost valuation, hard reject, auto rack-pick; cancel reverses placements — DONE

Extensions for the selling v3 OrderService (Flow A — design: plans/brainstroming.md), all
scoped to the ORDER kind (other kinds unchanged):

- **Proto** — `TransactionCreateResponse` gained `repeated TransactionCostItem items`
  (`product_id, count, unit_cost, total_cost`): the ORDER kind returns the average-cost
  valuation of the stock-out (the order service derives cross-team product fees from it);
  other kinds echo the caller price.
- **[inventory/transaction_order.go](../inventory/transaction_order.go)** (new) —
  `applyOrderOutbound`: FOR-UPDATE locks the `stock_states` rows, **hard-rejects**
  insufficient ready stock (`FailedPrecondition` — no negative guard exists in the shared
  engine, deliberately: problem/adjustment/transfer legitimately go negative), values
  items + `ChangeAmount` at `stock_ready_amount/stock_ready` (caller price ignored — it's
  the SELLING price and would corrupt StockState with margin), then
  `applyOrderPlacements`: per product reads `ProductConfig.placement_picking` (default
  SMALLER), drains racks `count ASC|DESC` greedily, applies
  `ApplyExplicitPlacements(..., ORDER_CREATED, ...)` — the placement logs double as the
  warehouse **pick list**. Placement shortfall vs state (pre-existing drift) consumes
  what exists (Opname corrects) — not an error.
- **[inventory/transaction_cancel.go](../inventory/transaction_cancel.go)** — now also
  calls `ReverseTransactionPlacements(..., ORDER_CANCELED, ...)` (idempotent, no-op for
  transactions without placements) — without it, canceled orders would leak placement
  decrements.
- **Tests** — transaction_test.go reworked: order subtests seed stock via restock first
  (ordering against empty stock now correctly rejects); new coverage: avg-cost valuation +
  response items + caller-price-ignored, SMALLER default picking with cross-rack spill,
  BIGGER config picking, placement-shortfall drift, short-stock + unknown-product rejects,
  cancel restores state AND placements (ORDER_CANCELED log, idempotent). Full inventory +
  mutations suites green.
- Side fix: [invoice_service/invoice_v2/overview.go](../../invoice_service/invoice_v2/overview.go)
  did not compile against the current schema (`total_payable/receivable` became message
  types in v2_overview.proto) — wrapped the sums in the new item messages
  (behavior-preserving bridge; the `change` breakdown stays empty until the owner's
  timeline work fills it).

---

## 2026-07-07 — Opname: per-line reason type (lost/broken/disaster) + note — DONE

Guideline addendum ("Under `Opnames`"): counted lines carry an optional **reason** + **note**
(owner-confirmed: per counted (rack, product) line, optional even on discrepancy).

- **Proto** — `OpnameReasonType {UNSPECIFIED, LOST, BROKEN, DISASTER}` in `types.proto`;
  `OpnameCountItem` += `reason` + `note` (≤300); `OpnameLineItem` += `reason`/`note`. `make proto-gen`.
- **Backend** — `InventoryOpnameLine` += `Reason`("lost"|"broken"|"disaster"|"") + `Note`; migration
  `00016_add_reason_to_opname_lines.sql`; `opname_line_count.go` persists both on update + insert paths;
  `opname_detail.go` returns them; reason mappers next to the status mappers. **Audit propagation**:
  `inventory_mutations.PlacementDelta` gained an optional `Note` (existing callers unchanged — zero value) and
  `ApplyExplicitPlacements` writes it to `StockPlacementLog.note`; `opname_complete.go` composes
  `"opname: <reason>[ — <note>]"` per delta, so Placement History explains why stock was adjusted.
- **Tests** — extended `opname_test.go`: reason/note round-trip via detail, re-count overwrite, and the composed
  placement-log notes on complete (`"opname: lost — one missing"`, `"opname: broken"`). Full inventory +
  mutations suites green.
- **Frontend** — `opnameApi.ts` re-exports `OpnameReasonType` + label/color maps (lost=red, broken=orange,
  disaster=purple); `CountRackDialog` rows gained Reason (`EnumSelect`, "No reason" default) + Note inputs;
  `OpnameSessionPage` lines table gained Reason (StatusTag) + Note columns. TS regen + `npm run build` clean.
- Run `cmd/tool migrate up` (inventory_service → 00016) before manual testing.

---

## 2026-07-07 — Opname (stock-take): full backend + real frontend wiring — DONE

The Opname feature (previously a frontend-only mock; docs' "9. Opname RPC" bullet was empty) is now real end-to-end.
Two design decisions (owner-confirmed): counting is **per (rack, product)** — the `StockPlacement` grain (the mock's
rack-total count can't reconcile per-product stock) — and Complete is **transaction-anchored** (mirrors
`acceptRestock`) rather than the synthetic txID-0 reconcile.

- **Proto** — `OpnameStatus` in `types.proto` (pending/completed/canceled); `Opname*` block in `service.proto`:
  `OpnameCreate` (seeds a PENDING session), `OpnameList` (flexible list, data types GENERAL + PROGRESS
  {line/counted/discrepancy counts}), `OpnameDetail` (header + per-(rack,product) lines with rack/product **names**
  joined server-side), `OpnameLineCount` (batch per rack; re-count overwrites; unknown product upserts an
  expected-0 line = found-but-not-expected), `OpnameComplete` → `{transaction_id}`, `OpnameCancel`. `make proto-gen`.
- **Models + migration** — [inventory_models/opname.go](../inventory_models/opname.go): `InventoryOpname` (+
  `InventoryTransactionID *uint64` set on complete), `InventoryOpnameLine` (unique `(opname_id, rack_id,
  product_id)`, `ExpectedCount` snapshot + `CountedCount`/`Counted`), `InventoryOpnameLog` audit table (no list RPC
  yet). New `InvTxOpname` transaction type. Migration `00015_create_inventory_opnames.sql`.
- **Handlers** (one per file, restock skeleton) — create seeds lines from `stock_placements` (`count > 0`) in one tx;
  list = flexible ids resolver + GENERAL/PROGRESS map fills; detail joins `racks` + `products`;
  line-count is pending-gated (`requirePendingOpname`); **complete** (the crux,
  [opname_complete.go](../inventory/opname_complete.go)): FOR-UPDATE locks the counted lines' live placements
  (rack,product-ordered), deltas = counted − current (counted lines only; uncounted untouched), mints
  `InventoryTransaction{opname}` + items (signed variance, valued at **StockState average price**), drives the
  aggregate via `NewProcessStockBatchLog` with a caller-signed `StockChange_Adjustment`, and applies per-rack deltas
  via `ApplyExplicitPlacements(..., ADJUSTMENT, ...)` — so every placement/batch log carries the opname's txID
  (reversible later via `ReverseTransactionPlacements`). Idempotent complete + cancel; cancel is pre-complete only,
  no stock effect. No caller identity yet (inventory doesn't mount the access interceptor) — `created_by_id` stays 0,
  mirroring restock.
- **Tests** — [opname_test.go](../inventory/opname_test.go): seeding excludes count-0 placements; re-count overwrite +
  expected-0 upsert; PROGRESS counts; complete applies counted values (placements + StockState, avg-price valuation,
  ADJUSTMENT logs anchored to the minted tx, delta-0 and uncounted skipped, idempotent re-complete); post-complete
  count/cancel rejected; cancel flow (no stock effect, idempotent, complete-after-cancel rejected); NotFound.
  `go test ./inventory/... -p 1` green.
- **Frontend** — [opnameApi.ts](../../warehouse_frontend/src/inventory/opnameApi.ts) is now thin wrappers over the
  real `inventoryClient.opname*` (bigint ids; the mock store/seed deleted; list fabricates a next-page `PageInfo`
  since the flexible list has no total). Session page lines are per (rack, product) with Rack/Product/Expected/
  Counted/Diff/Status columns; `CountLineDialog` → **CountRackDialog** (batch per-rack counting, blank = leave
  uncounted); `StartOpnameDialog` + `OpnameListPage` use `currentTeam.teamId` (bigint) instead of the `wh-<n>`
  string seam; the list's By column renders `UserName` (— when id 0). `npm run build` clean.
- **Verified**: inventory build/vet green; opname + full inventory/mutations tests green; frontend TS regen + build
  clean. Run `cmd/tool migrate up` (inventory_service → 00015) before manual testing.

### Known / pending (opname)
- `created_by_id`/`completed_by_id` are 0 until inventory mounts the v2 access interceptor (same gap as restock).
- `OpnameLogList` RPC deferred — the audit log table is written from day one.
- Reversal of a completed opname (via the anchored txID) is possible but not exposed.

---

## 2026-07-07 — Rack Detail: Products + Histories tabs (guideline "Under Racks") — DONE

Two new **rack-scoped** RPCs power the Rack Detail tabs the owner added to the frontend guideline. Chosen over relaxing
`ProductPlacementList`/`ProductPlacementLog` (both REQUIRE `product_id > 0` and their items carry no `product_name`, which
a rack-wide view needs).

- **Proto** — `RackProductList` + `RackHistory` RPCs on `InventoryService` (+ messages) in
  `schema/inventory_iface/v1/service.proto`, `make proto-gen`. Standard list convention (`PageFilter` req / `PageInfo`
  resp); `RackHistory` takes `common.v1.TimeFilter time_range` (epoch micros, like `ProductPlacementLog`). Items carry
  `product_name` (joined).
- **Handlers** — [rack_product_list.go](../inventory/rack_product_list.go): `stock_placements` on the rack with `count > 0`
  `LEFT JOIN products`, ordered by count desc. [rack_history.go](../inventory/rack_history.go): `stock_placement_logs`
  on the rack across all products, `LEFT JOIN products`, `created_at` window, id desc. Both via
  `db_connect.SetPaginationQuery` (wraps the joined builder as a count subquery, so `Table(...).Joins(...).Select(...)` +
  `Scan` works). No manual registration — the generated `NewInventoryServiceHandler` covers them.
- **Tests** — [rack_tabs_test.go](../inventory/rack_tabs_test.go): `TestRackProductList` (count=0 / other-rack /
  other-warehouse excluded; names + count-desc order; note `StockPlacement` is unique on (product,warehouse,rack)) and
  `TestRackHistory` (before/after-window + other-rack rows excluded; names resolve). `-run TestRack -p 1` green.
- **Frontend** — [inventory/RackDetailPage.tsx](../../warehouse_frontend/src/inventory/RackDetailPage.tsx) gained a
  **Products** tab (rows click → `/product/:id`) and a **Histories** tab (From/To date filter, default 7 days →
  `TimeFilter` micros). Extracted the shared `STOCK_CHANGE_LABELS` (incl. `MOVE`) + `ymd`/`daysAgo`/`today`/`rangeMicros`
  helpers into [lib/inventoryHistory.ts](../../warehouse_frontend/src/lib/inventoryHistory.ts), now used by both
  RackDetailPage and ProductDetailPage (dedup). `npm run build` clean.

---

## 2026-07-07 — Restock accept: landed-cost batch pricing (guideline §5) — DONE

Implemented the new pricing rule in [restock-implementation.md](restock-implementation.md) §5:
`price_item_batch = price_per_item_accepted + ((shipping_fee + warehouse_accept_fee) / piece_that_accepted)`.

- **Backend** ([inventory/restock_update.go](../inventory/restock_update.go), `acceptRestock` only) —
  single-lever design: after the effective-counts loop, `perPiece = (ShippingCost + fee) /
  totalEffective` is added to each minted item's price. Because the `items` slice feeds the
  `InventoryTransactionItem` rows, the `ChangeAmount` (→ `StockState.StockReadyAmount` +
  `StockBatchLog`), and `mintTransactionBatches` (→ `StockBatch.Price`), the whole value chain is
  landed and mutually consistent with no changes to the mutation processors. Denominator = total
  effective pieces of the whole restock (flat per-piece surcharge, same semantic as legacy
  `RestockCost.PerPieceFee`); problem pieces absorb none of the fees but the accepted pieces absorb
  100% of shipping+fee. The `warehouse_accept_fee` read was hoisted above the loop and a negative fee
  is now rejected (`InvalidArgument`). Downstream: ProductDetail batch tab shows landed prices
  automatically; transfer source averages (`StockReadyAmount/StockReady`) carry landed value forward.
  **Intentionally purchase-price**: RestockDetail/RestockList amounts + item prices (document
  economics) and `OngoingRestockAmount` (pending docs).
- **Tests** ([inventory/restock_test.go](../inventory/restock_test.go)) — the accept subtest's batch
  price 10 → **1810** (10 + 9000/5); added transaction-item price + new `stockAmountOf` helper
  asserting `StockReadyAmount` = **9050**, stable across the re-accept no-op (guards double-pricing);
  RCP-6 fee subtest now proves the fee participates: batch price **9760** (10 + (12000+7500)/2);
  negative-fee rejection case added to accept validations.
- **Frontend** ([inbound/RestockAcceptPage.tsx](../../warehouse_frontend/src/inbound/RestockAcceptPage.tsx),
  developer-chosen live preview) — each Acceptance Summary product row gained a muted
  "Batch price: Rp …" line computed live from the fee input and problem counts
  (`item.price + (shippingCost + acceptFee)/effective`), hidden in the transient all-problem state.
- **Verified** — build/vet clean; `TestRestock` + full inventory & inventory_mutations suites green
  (`-p 1`); `npm run build` clean. No proto/schema/migration changes.

---

## 2026-07-07 — ProductDetail: ongoing restock + transfer counts (guideline #6/#7) — DONE

Implemented the "ongoing (in-transit) inbound/outbound" info for a product+warehouse on
[ProductDetail](../inventory/product_detail.go), per the frontend guideline's Product Detail Page items 6/7.

- **Proto** — added six fields to `ProductDetailResponse` (`schema/inventory_iface/v1/service.proto`, `make proto-gen`):
  `ongoing_restock_count/amount`, `ongoing_transfer_out_count/amount`, `ongoing_transfer_in_count/amount`.
- **Handler** — three GORM aggregations scoped by `product_id` + `warehouse_id`, **pending docs only** (accepted/canceled
  are already in `StockState`, so excluded): restock = `inventory_restock_items JOIN inventory_restocks` where
  `status IN ('pending','problem')`; transfer-out = `inventory_transfer_items JOIN inventory_transfers` where
  `from_warehouse_id = W AND status='pending'`; transfer-in = same with `to_warehouse_id = W`. Each returns
  `SUM(count)` / `SUM(count*price)`.
- **Return is intentionally omitted** — inventory_service has **no return document/workflow** (a return is only an
  immediate `TransactionReturn`), so there is no "ongoing return" to aggregate. Deferred until a return workflow exists.
- **Test** — [product_detail_test.go](../inventory/product_detail_test.go) extended: seeds a pending restock + pending
  transfer out/in (and an accepted restock + canceled transfer that must NOT count, plus an other-product row) and asserts
  the six fields. `go test ./inventory/... -run TestProductDetail -p 1` green.
- **Frontend** (same change set) — ProductDetailPage gained: an **Ongoing** InfoCard (restock / transfer out / transfer
  in); a **Placement History** tab (`ProductPlacementLog`) with rack + 7-day time filters; a real **Stock History** tab
  (`StockMovement`, value-carrying); Placements rows now show rack names, a **Move** action (wires the existing
  `PlacementMoveDialog`), and click-a-row → Placement History filtered by that rack. `npm run build` clean.

---

## 2026-07-06 — PlacementMove: intra-warehouse rack-to-rack stock move — DONE

Implements the `PlacementMove` RPC from [readme.md](readme.md) §6 / "RPC Placements" — the first
dedicated placement **write** RPC (placements previously mutated only via restock-accept / reversal /
reconcile).

- **Proto** ([schema/inventory_iface/v1/service.proto](../../schema/inventory_iface/v1/service.proto),
  [types.proto](../../schema/inventory_iface/v1/types.proto)) — new `rpc PlacementMove` on
  `InventoryService`; `PlacementMoveRequest{warehouse_id, repeated PlacementMoveItem{product_id,
  from_rack_id, to_rack_id, count}, note}` + empty `PlacementMoveResponse`; new enum
  `STOCK_CHANGE_TYPE_MOVE = 9`; `note` (field 11) added to `ProductPlacementLogItem`. `make proto-gen`.
- **Migration** — `db_migrations/00014_add_note_to_stock_placement_logs.sql` adds
  `note TEXT NOT NULL DEFAULT ''` (owner-approved). `StockPlacementLog.Note` added to
  [inventory_models/placement.go](../inventory_models/placement.go). **Pending: owner runs
  `go run ./cmd/tool migrate up` (Local → inventory_service)** — interactive; tests use AutoMigrate.
- **Mutation** — new [inventory_mutations/placement_move.go](../inventory_mutations/placement_move.go):
  `ApplyPlacementMove(tx, warehouseID, deltas, note, at)` reuses `lockOrCreateStockPlacement` and the
  existing `PlacementDelta`; sorts legs by `(rack_id, product_id)` for deadlock-safe lock ordering;
  **new negative guard** on outgoing legs → typed `ErrInsufficientPlacement`; writes MOVE logs with
  note. `ApplyExplicitPlacements` and its callers (acceptRestock, ReverseTransactionPlacements) are
  untouched.
- **Handler** — new [inventory/placement_move.go](../inventory/placement_move.go): manual defensive
  validation (InvalidArgument: ids/count > 0, `from != to`, min 1 item, **duplicate (product, from, to)
  rejected**), one `Transaction` doing rack validation (copied from acceptRestock: live rack of the
  warehouse) then `ApplyPlacementMove`; `ErrInsufficientPlacement` → `FailedPrecondition`.
  [product_placement_log.go](../inventory/product_placement_log.go) now maps `note`.
- **Decisions** — no `InventoryTransaction` minted (move is net-zero for the warehouse;
  StockState/StockBatch untouched; `TransactionID`/`UserID` = 0, auth stays open); emptied source rows
  are kept at Count 0 (existing behavior). **Caveat:** a chain move (A→B then B→C) in one request only
  succeeds in submission order (stable sort keeps same-rack legs in request order) — unreachable from
  the UI, which sends single-item requests.
- **Tests** — [inventory/placement_move_test.go](../inventory/placement_move_test.go), 9 subtests:
  single partial move + paired MOVE logs/note/balance, multi-item, move-all keeps source row,
  note via ProductPlacementLog, cumulative-overdraft rollback, single overdraft, no-source-row,
  rack scoping (foreign + deleted), validation batch.
- **Frontend** — TS regen; new page-local
  [products/PlacementMoveDialog.tsx](../../warehouse_frontend/src/products/PlacementMoveDialog.tsx)
  (RackFormDialog pattern; destination via unmodified shared `RackPicker`; count clamped to the row
  balance; note); [ProductDetailPage.tsx](../../warehouse_frontend/src/products/ProductDetailPage.tsx)
  Placements tab gains a per-row **Move** button (disabled at 0), Stock History gains a **Note** column,
  `MOVE → "Move"` label; `onMoved` refreshes detail + active tab.
- **Verified** — `make proto-gen` + `schema` build clean; inventory_service `go build`/`go vet` clean;
  `go test ./inventory -run TestPlacementMove -p 1` and full inventory + inventory_mutations suites
  green; `make build-mcp` (omnibus) compiles; `npm run build` clean. **Pre-existing unrelated failure:**
  `cmd/app_production/TestSyncLegacy` still panics (owner WIP, see prior entry) — untouched.

---

## 2026-07-06 — restock_cancel is now pre-accept only (spec rule 2 recheck) — DONE

Recheck of [restock-implementation.md](restock-implementation.md) rule 2, which lists
`restock_cancel` among the actions "only available when restock not accepted". The implementation
allowed canceling an ACCEPTED restock (full reversal of state + batch + placements) — now aligned:

- **Backend** ([inventory/restock_update.go](../inventory/restock_update.go)) — `cancelRestock`
  returns `FailedPrecondition` ("restock is already accepted") on an accepted restock; the whole
  reversal branch (reconstructCancel + StockBatch delete + `ReverseTransactionPlacements` +
  transaction cancel) is removed. Cancel stays a pure status flip for PENDING/PROBLEM and stays
  idempotent on CANCELED. Side benefit: the un-reversed `warehouse_accept_fee` ledger concern is
  moot — an accepted restock's transaction is untouchable through this action. (`reconstructCancel`
  / `ReverseTransactionPlacements` remain in use by TransactionCancel / TransferCancel.)
- **Tests** ([inventory/restock_test.go](../inventory/restock_test.go)) — the spec change forced
  reworking the existing "cancel accepted reverses …" scenario into **"cancel after accept is
  rejected"** (asserts FailedPrecondition and that document/stock/batch/placements/transaction are
  intact); log chronology drops the trailing CANCELED; the no-op-second-cancel +
  accept-after-cancel coverage moved into "cancel pending is a status flip only"; the
  `invertory_histories`/`skus` migrations (only needed by the old reversal path) removed from setup.
- **Frontend** — warehouse detail page ([inbound/RestockDetailPage.tsx](../../warehouse_frontend/src/inbound/RestockDetailPage.tsx))
  no longer shows **Cancel** for ACCEPTED (PENDING/PROBLEM only, matching the selling page which
  was already compliant); the "reverses the entered stock…" dialog copy removed; stale comments in
  inboundApi.ts / procurement/restockApi.ts corrected.
- **Proto** — comment-only fixes on the RestockCreate/RestockUpdate service block and
  `RestockCancel` message; `make proto-gen` + frontend TS regen.
- **Verified** — `go build`/`go vet` clean; `go test ./inventory/... -run TestRestock -p 1` and the
  full inventory + inventory_mutations suites green; `npm run build` clean (×2, incl. after TS
  regen). **Pre-existing, unrelated failure:** `cmd/app_production/TestSyncLegacy` panics because
  `NewSyncLegacyFunc` was rewritten into a CLI/HTTP-client action (`c.String("host")` on the nil
  `*cli.Command` the test passes) — owner WIP, left untouched.

---

## 2026-07-06 — RestockAccept: placements required + warehouse_accept_fee → payable — DONE

Tightened `RestockAccept` per [restock-implementation.md](restock-implementation.md) §5.

- **Placements now REQUIRED** — `acceptRestock`
  ([inventory/restock_update.go](../inventory/restock_update.go)) runs the placement accounting
  unconditionally (dropped the `if len(placements) > 0` guard). Every effective unit must be placed:
  `placed + problem == ordered` per product, else `InvalidArgument`. An empty/short placements list
  now fails (rack-belongs-to-warehouse + product-in-restock checks unchanged). The accept UI already
  enforced full allocation, so no frontend regression.
- **`warehouse_accept_fee`** — new `double` on `RestockAccept` (and echoed on
  `RestockDetailResponse`). When `> 0`, after the status flip `acceptRestock` posts it **atomically in
  the same tx** via `invoice_v2.PostBalanceLog(tx, restock.WarehouseID /*creditor*/,
  restock.TeamID /*debtor*/, WAREHOUSE_FEE, fee, RECEIVABLE, …, 0, now)` → warehouse RECEIVABLE `+fee`,
  selling team the mirrored PAYABLE `−fee`. Idempotent (re-accept returns early). The fee is stored on
  `InventoryRestock.WarehouseAcceptFee` (model + goose `00013_add_restock_warehouse_accept_fee.sql`)
  and surfaced on both restock detail pages when `> 0`.
- **First in-tx cross-service ledger write.** inventory_service now imports
  `github.com/pdcgo/invoice_service/invoice_v2` (+ transitive `user_service`) — resolves locally via
  `go.work`. Deploy caveat: `invoice_service` must be **published at a version that contains
  `invoice_v2`** (tag `v1.0.10` predates it); `deploy/build-inventory-service` in the
  [Makefile](../../Makefile) now `go get`s both, and the module `require` stays workspace-only until
  then (a broken `go mod tidy` otherwise, since v1.0.10 lacks the package).
- **Frontend** — accept page ([inbound/RestockAcceptPage.tsx](../../warehouse_frontend/src/inbound/RestockAcceptPage.tsx))
  gained a **Warehouse Accept Fee** `CurrencyInput` in the summary; `acceptRestock` sends
  `warehouseAcceptFee`; both detail pages show a **Warehouse Fee** stat when set.
- **Verified** — `make proto-gen` clean; `go build`/`go vet`/`go test ./inventory/... -run TestRestock -p 1`
  green (new subtests: placements-required negative + fee-posting double-entry assertions, migrating the
  invoice balance tables); frontend TS regen + `npm run build` clean.

---

## 2026-07-06 — Warehouse-team Restock frontend & flow (accept UI) — DONE

Built the warehouse side of the restock flow on RestockUpdate v2 (frontend only; no backend changes).

- **inboundApi** ([warehouse_frontend/src/inbound/inboundApi.ts](../../warehouse_frontend/src/inbound/inboundApi.ts)) —
  detail mapping gained `productId` + per-item `problemCount/problemNote` + purchase info
  (order id / shipping / ongkir / payment / `inventoryTransactionId`); new v2 batch wrappers
  `acceptRestock` (one `restockAccept{placements, problems}` action), `cancelInboundRestock`,
  `setRestockMarker` (PENDING↔PROBLEM + note). (The selling-side v2 wrapper fixes in
  procurement/restockApi.ts were done by the owner.)
- **New shareable `RackPicker`** (components/RackPicker.tsx; gallery + shareable_components.md
  entry per the standing rule) — warehouse-scoped Select over RackList GENERAL.
- **List** — warehouse Inbound → Restocks gained an **All Restocks** tab (StatusTabs `allLabel`
  opt-in; Returns unaffected).
- **Detail** ([inbound/RestockDetailPage.tsx](../../warehouse_frontend/src/inbound/RestockDetailPage.tsx)) —
  purchase-info stats (shipping name via publicShipmentList), per-item problem badges, History
  Timeline (log helpers reused from procurement/restockApi), and status-driven actions:
  PENDING/PROBLEM → **Accept** (navigates to the accept page), **Mark Problem/Pending** (dialog +
  note), **Cancel** (ConfirmDialog); ACCEPTED → Cancel only with reversal warning copy.
- **Accept page** ([inbound/RestockAcceptPage.tsx](../../warehouse_frontend/src/inbound/RestockAcceptPage.tsx),
  route `/inbound/restock/:restockId/accept`; developer-chosen dedicated page) — per product:
  problem count+note inputs and repeatable RackPicker placement rows with a live
  **remaining = ordered − problem − placed** indicator; sticky Acceptance Summary; the Accept
  button enables only when every product is fully allocated (**placements required in the UI**,
  developer-chosen; the backend's no-placement path stays API-only); guards redirect when the
  restock is already accepted/canceled.
- **Verified** — `npm run build` (tsc + vite) clean. Manual flow: selling creates → warehouse
  Pending tab → detail → accept with problems+placements → detail shows ACCEPTED + problem badges
  + Timeline; cancel reverses.

### Known / open
- Warehouse Return pages remain mock. Selling-side edit page wiring
  (`/inventory/restock/:restockId/edit`) is the owner's WIP.

---

## 2026-07-06 — RestockUpdate v2: batch actions + accept with placements/problems — DONE

Reworked `RestockUpdate` to the new spec in [restock-implementation.md](restock-implementation.md):
a **batch** request (`repeated RestockUpdateAction actions`, applied in order, all-or-nothing) with
granular actions `change_status | update_items | update_shipping_fee | update_shipping_info |
restock_cancel | restock_accept`.

- **Proto** — old `RestockEdit`/`RestockProblem`/single-oneof request removed; new action messages
  (`ChangeStatus{status: PENDING|PROBLEM, note}`, `UpdateItems`, `UpdateShippingFee{shipping_cost}`,
  `UpdateShippingInfo{shipping_id, receipt, extern_order_id, payment_type}`, `RestockCancel`,
  `RestockAccept{placements, problems}` with `RestockPlacementItem{product_id, rack_id, count}`).
  `RestockDetailResponse.transaction_id` → `inventory_transaction_id` (same field number).
- **Model / migration** — `InventoryRestock.InventoryTransactionID *uint64` (nil = not accepted);
  **`00009` edited in place** (developer-chosen "modify current migration"):
  `transaction_id BIGINT NOT NULL DEFAULT 0` → nullable `inventory_transaction_id BIGINT`.
  Dev DBs that already ran 00009 must re-migrate (down/up).
- **Behavior / decisions (developer-confirmed)**
  - The four update actions are **pre-accept only** (FailedPrecondition once accepted or canceled).
  - `change_status` = PENDING↔PROBLEM **marker only**; accepted/canceled stay with their actions.
  - **Accept: problem goods are EXCLUDED from stock** — effective = ordered − problem enters
    StockState/StockBatch; problems land on the item rows. Placements are optional; when present
    they must fully balance (`placed + problem == ordered` per product) into live racks of the
    restock's warehouse (validated).
  - New `inventory_mutations` helpers: `ApplyExplicitPlacements` (explicit per-rack deltas +
    StockPlacementLog; the existing processor only re-derives from legacy `invertory_histories`)
    and `ReverseTransactionPlacements` (net-per-rack reversal, net-zero idempotent) — cancel of an
    accepted restock now reverses **state + batch + placements**.
  - One audit-log row per executed action (`edited`×N / `problem` / `accepted` / `canceled`).
- **Verified** — `make proto-gen` clean; build/vet clean; reworked `TestRestock` green (batch
  ordering + in-memory state chaining, accept with placements/problems incl. balance/rack/product
  validations + idempotency, cancel reverses placements to zero, post-accept guards, log
  chronology `[created, edited×3, problem, edited, accepted, canceled]`); full inventory +
  inventory_mutations suites green; omnibus builds; frontend TS regen + `npm run build` clean
  (no UI caller of restockUpdate).

### Known / open
- No frontend UI for the new actions yet (selling detail stays read-only; warehouse accept UI with
  placements is the natural next surface).
- Auth still open; `created_by_id` = 0 in logs and documents.

---

## 2026-07-06 — Restock audit log (RestockLogList + Timeline) & per-item problems — DONE

Implemented the readme's `RestockLogList` (audit) and the implementation-guideline "Restock Detail
Page Context" (Timeline history + item problem detailed in item). Full spec now lives in
[restock-implementation.md](restock-implementation.md) (linked from the readme).

- **Proto** — `RestockLogAction` enum; `RestockProblem` += `items[{product_id, count, note}]`;
  `RestockDetailItem` += `problem_count`/`problem_note`; new `RestockLogList` RPC — **flat
  chronological paginated log** (ProductPlacementLog precedent, developer-chosen) with real PageInfo.
- **Model / migration** — `InventoryRestockLog` + problem columns on `inventory_restock_items`;
  goose `00012_add_restock_logs_and_item_problems.sql` (**confirmed** per the migration HARD RULE).
- **Behavior / decisions**
  - `appendRestockLog` writes one row per lifecycle event **inside the event's transaction**
    (created/edited/accepted/problem/canceled); idempotent no-ops and rejected actions record nothing.
  - `problem.items` update the matching item rows (product not in the restock → InvalidArgument);
    the document-level note append stays.
  - No backfill: logs start at deploy (developer-confirmed).
- **Frontend** — selling [RestockDetailPage](../../warehouse_frontend/src/procurement/RestockDetailPage.tsx):
  new **History block — first Chakra v3 `Timeline` usage in the repo** (colored indicator per action,
  note, timestamp) fed by `listRestockLogs`; items show a red **Problem** badge + count + note when set.
- **Verified** — `make proto-gen` clean; build/vet clean; `TestRestock` extended (chronological
  `[created, edited, accepted, canceled]` log with no duplicates from idempotent calls, per-item
  problem round-trip via detail, unknown-product problem → InvalidArgument, log NotFound); full
  `./inventory/...` suite green; omnibus builds; frontend `npm run build` clean.

### Known / open
- Actor (`created_by_id`) still 0 — auth open. No frontend problem-marking UI (display-only).
- Warehouse inbound detail page doesn't render the Timeline/problem blocks yet.

---

## 2026-07-04 — Restock purchase fields + selling-team create/list/detail UI — DONE

Extended the Restock document with the selling-team purchase info and shipped the full selling
`Inventory → Restocks` experience (per warehouse_frontend implementation-guideline "Under Restock
submenu in Selling Team").

- **Proto** — `RestockCreateRequest` / `RestockEdit` / `RestockDetailResponse` gained
  `extern_order_id`, `shipping_id` (courier), `shipping_cost` (ongkir), `payment_type`
  (**reuses `warehouse_iface.v1.PaymentType`**, no new enum).
- **Model / migration** — `InventoryRestock` + the 4 fields (payment stored as
  `''|'shopeepay'|'transfer'`); goose `00011_add_restock_order_fields.sql` (additive ALTER,
  **confirmed by the developer** per the migration HARD RULE).
- **Handlers** — create persists, `edit` replaces, detail returns; `paymentTypeToModel/ToProto`
  mappers. `TestRestock` extended to round-trip the fields through create/detail/edit; full
  `./inventory/...` suite green; omnibus builds.
- **Frontend (selling procurement went real)** — `procurement/restockApi.ts` mock replaced with
  `restockList/restockDetail/restockCreate`; `RestockFormDialog` **deleted** (create is a page now).
  - List: tabs **All Restocks / Pending / Accepted / Problem / Canceled**, search, rows →
    `/inventory/restock/:restockId`.
  - New detail page (order id / receipt / shipping name via `publicShipmentList` / ongkir /
    payment label / status / items).
  - New **create page** `/inventory/restock/new` — first Chakra v3 `Steps` usage: ① warehouse
    (warehouse-locked TeamPicker) ② products (ProductPicker + count/price lines, names via
    `ProductByIDs`) ③ order id / receipt / ShippingPicker / ongkir / PaymentTypePicker ④ note —
    with a sticky right-side preview (ongkir, total product, amount, per-line preview).
  - `npm run build` clean.

### Known / open
- Selling detail page is read-only (no cancel/edit actions yet); supplier is not captured by the
  create flow (sent empty; backend still supports it).
- The warehouse inbound pages don't yet display the new purchase fields.

---

## 2026-07-04 — Transfer Stock Between Warehouse RPCs + warehouse Transfers UI — DONE

Implemented the readme "Transfer Stock Between Warehouse RPC" section (`TransferCreate/Cancel/Accept/
Detail/List`) as a **two-legged, in-transit workflow document**, plus a new read-only warehouse UI.

- **Proto** — `TransferStatus` (PENDING/ACCEPTED/CANCELED) in [types.proto](../../schema/inventory_iface/v1/types.proto);
  5 RPCs + messages in [service.proto](../../schema/inventory_iface/v1/service.proto). `TransferItem` carries
  **no price** (derived); `TransferList` follows the flexible list convention (GENERAL with from/to warehouse
  names + TOTAL, sort oneof, ids); `filter.warehouse_id` matches **either side**.
- **Model / migration** — `inventory_models.InventoryTransfer` + `InventoryTransferItem`
  ([transfer.go](../inventory_models/transfer.go)); new `InvTxTransferOut`/`InvTxTransferIn` transaction types;
  goose `00010_create_inventory_transfers.sql` (**new tables confirmed by the developer** per the migration HARD RULE).
- **Behavior / decisions**
  - **Two-legged in-transit (per legacy `WarehouseTransfer` semantics)**: create applies the OUT leg at the
    source immediately (stock exits; `applyTransferLeg` with the caller-signed `Transfer` reason, sign −1);
    accept applies the IN leg at the destination (sign +1) **and mints the `StockBatch`** there
    (`mintTransactionBatches` reuse); both legs are linked on the document
    (`out_transaction_id`/`in_transaction_id`).
  - **Price derived from source** (developer-chosen): per-product `stock_ready_amount / stock_ready` at create
    (0 when no stock) — value conserved; stored on the items.
  - **Cancel is pre-accept only** (legacy: a cancel never touches the IN leg): reverses the OUT leg via
    `reconstructCancel` + marks the OUT transaction canceled; accepted → FailedPrecondition ("send a transfer
    in the opposite direction instead"). Accept + cancel are idempotent.
  - No stock-sufficiency guard at create (engine-wide convention; StockState may go negative).
- **Frontend** — new warehouse nav leaf **Inventory → Transfers** (`/inventory/transfer`, new `TransferIcon`),
  read-only [TransferListPage](../../warehouse_frontend/src/inventory/TransferListPage.tsx) (status tabs
  Pending/Accepted/Canceled; From/To/Created/Items/Amount/Status; either-side filter by the sidebar warehouse
  team id; result-count pagination heuristic) + [TransferDetailPage](../../warehouse_frontend/src/inventory/TransferDetailPage.tsx)
  (header stats + items with product names).
- **Verified** — `make proto-gen` clean; inventory_service build/vet clean; omnibus `cmd/app_development`
  builds; `TestTransfer` green (create-moves-out + derived prices + no source batch, accept-moves-in + dest
  batch at derived price + idempotent, cancel-pending restores + idempotent + accept-after-cancel rejected,
  cancel-accepted rejected, detail names/totals, list either-side/direction/status filters + sorts + maps,
  validations + NotFound); full `./inventory/...` suite green; frontend `npm run build` clean.

### Known / open
- **No create/accept/cancel UI actions** (list/detail read-only); no MCP tools; auth open
  (`created_by_id` = 0). Per-rack `StockPlacement` still skipped (v1 transaction decision).
- Derived price is 0 for products with no source stock — the OUT/IN legs then move counts with zero value.

---

## 2026-07-04 — Restock RPCs (Create / Update / Detail / List) + warehouse inbound UI — DONE

Implemented the readme "Restock Related RPC" section as a **workflow document layer** on top of the
transaction stock engine, plus wired the warehouse frontend inbound pages to the real RPCs.

- **Proto** — `RestockStatus` enum (PENDING/ACCEPTED/PROBLEM/CANCELED) in [types.proto](../../schema/inventory_iface/v1/types.proto);
  4 RPCs + messages in [service.proto](../../schema/inventory_iface/v1/service.proto). `RestockUpdate` is an
  action oneof (`accept | problem | cancel | edit`); `RestockList` follows the flexible list convention
  (GENERAL/TOTAL data types + sort oneof + `map<uint64,item>` + `ids`). Fixed the readme's `Restockreate` typo.
- **Model / migration** — `inventory_models.InventoryRestock` + `InventoryRestockItem`
  ([restock.go](../inventory_models/restock.go)); goose `00009_create_inventory_restocks.sql`
  (**new tables confirmed by the developer** per the database-schema migration HARD RULE).
- **Behavior / decisions**
  - **Accept mutates** (developer-chosen): `RestockCreate` = PENDING document, NO stock effect.
    `accept` mints an `InventoryTransaction{restock}` + items, applies the `StockChange` via
    `NewProcessStockBatchLog` and mints the `StockBatch` (`mintTransactionBatches` reuse), links
    `TransactionID` + `accepted_at`. Idempotent (re-accept no-ops).
  - `cancel`: PENDING/PROBLEM → status flip; ACCEPTED → `reconstructCancel` reversal + batch void +
    underlying transaction canceled (same path as TransactionCancel). Idempotent.
  - `problem` = marker (note appended); still acceptable afterwards. `edit` = PENDING only
    (header + item replacement), else FailedPrecondition.
- **Frontend** — [inbound/inboundApi.ts](../../warehouse_frontend/src/inbound/inboundApi.ts) Restock
  mock bodies replaced with real `inventoryClient.restockList/restockDetail` (UI types kept; proto
  status mapped; result-count PageInfo heuristic since the guideline response has no total). Status
  vocabulary + tabs gained **Canceled**; detail items show the product name (SKU column dropped);
  warehouse id = the sidebar warehouse team id (as the Rack pages). Return stays mock.
- **Verified** — `make proto-gen` clean; inventory_service `go build`/`go vet` clean; omnibus
  `cmd/app_development` builds; `TestRestock` green (pending-no-stock, edit, accept+batch+idempotent,
  cancel-accepted reversal+idempotent, cancel-pending, problem→accept, detail+names, list
  filters/sorts/maps, NotFound) + full `./inventory/...` suite green; frontend `npm run build` clean.

### Known / open
- **RestockCreate/Update have no UI yet** (list/detail wiring is read-only); the selling-side
  `procurement/restockApi.ts` mock is still fake.
- **Auth stays open** (no request_policy/use_scope anywhere in inventory_iface — consistent with the
  service's other RPCs); `created_by_id` is 0 (handlers don't read identity).
- No MCP tool wiring. Per-rack `StockPlacement` still skipped on accept (v1 transaction decision).

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

---

## 2026-08-10 — Warehouse accept fee gets ledger attribution

### Restock fee now records what caused it — DONE
- The warehouse accept fee posted in `acceptRestock` was the last ledger writer in this service with no source
  row, so its legs were unattributable in invoice v2 (`GetBalanceChangeSource` returned nothing for them).
- `invoice_v2.PostBalanceLog` changed its optional source parameter from `...*OrderSource` to a `...LedgerSource`
  interface, so causes other than orders can be attached. Call sites are unaffected — `*OrderSource` satisfies it.
- `acceptRestock` now passes `&invoice_v2.RestockSource{TxID: txID, TeamID: restock.TeamID, WarehouseID:
  restock.WarehouseID}`. `TxID` is the **inventory transaction**, not the restock document id the note carries;
  `TeamID` is the charged team, identical on both legs so the pair joins as one unit.
- Files: [inventory/restock_update.go](../inventory/restock_update.go),
  test [inventory/restock_test.go](../inventory/restock_test.go) (migrates `BalanceChangeRestockSource`).
- Verified: `go test ./... -p 1` green except the pre-existing `TestSyncLegacy` panic below (confirmed unchanged
  by stashing this work and re-running).

### Known / pending
- Historical fee legs stay unattributed until invoice_service's `backfill-sources` command is run; it recovers
  this service's legs by parsing the `"restock %d warehouse accept fee"` note and resolving the restock document
  id to its `inventory_transaction_id`.
- **Pre-existing, unrelated:** `TestSyncLegacy` (cmd/app_production) still panics on a nil `*cli.Command` in its
  own setup.

---

## 2026-09-01 — StockMovement: a selling-side view, value, and two rollups

### Contract — DONE
- `StockMovementRequest` is **unchanged** — `warehouse_id` stays required. The warehouse-scoped RPC keeps its
  contract; spanning warehouses is a separate RPC rather than a loosened rule on the existing one.
- Added to [schema/inventory_iface/v1/service.proto](../../schema/inventory_iface/v1/service.proto), regenerated
  with `make proto-gen`:
  - `StockMovementSelling` — the selling-side view of the same log, `warehouse_id` optional (0 = every warehouse
    the product sits in). Reuses `MovementItem`, so the row shape matches `StockMovement` exactly.
  - The three new requests take `common.v1.TimeFilterRange` (`google.protobuf.Timestamp`) rather than the
    epoch-microsecond `TimeFilter` the original `StockMovement` still uses.
  - `StockMovementDaily` — per-day rollup.
  - `StockMovementBreakdown` — split by change type.
- `DailyMovementItem` carries flow (`total_in`/`total_out`, `amount_in`/`amount_out`) and the day's closing
  position (`balance_count`/`balance_amount`/`price`). `total_out` is a positive magnitude so a chart can stack
  it against `total_in` without flipping signs.
- `MovementBreakdownItem` carries `change_count`/`change_amount`/`transaction_count` per `StockChangeType`. It is
  a flow, not a position — there is no per-change-type balance — and is unpaginated, bounded by the enum.

### Handlers — DONE
- Each handler builds its own query inline (owner's call — no shared scope/list helper), so
  [inventory/stock_movement.go](../inventory/stock_movement.go) keeps its old shape plus two columns. The
  selling views skip the warehouse predicate when the id is 0; `StockMovement`'s `gt = 0` rule is enforced
  by the `validate` interceptor in `custom_connect.NewDefaultInterceptor`.
- Both now select `price` and `balance_amount`; both were already in the proto and in `stock_batch_logs`, just
  never read.
- [inventory/stock_movement_daily.go](../inventory/stock_movement_daily.go) aggregates in **two steps**: the
  inner query takes each warehouse's last balance of the day (`array_agg(... ORDER BY id DESC)[1]`), the outer
  sums those across warehouses. Summing raw balances in one pass would multiply-count a product held in several
  warehouses. Days are cut in Asia/Jakarta and returned as the instant of that midnight.
- Daily `price` is derived, not stored: `balance_amount / balance_count` at close (a day holds many per-unit
  prices). 0 when stock is 0.
- [inventory/stock_movement_breakdown.go](../inventory/stock_movement_breakdown.go) groups by change type using
  `SUM(change)` and `SUM(change * price)` — `price` is per-unit and non-negative, so the sign follows `change`.

### Known / pending
- **Tests written but not executed** — [inventory/stock_movement_agg_test.go](../inventory/stock_movement_agg_test.go)
  covers both rollups, the unscoped-warehouse balance rule, `StockMovementSelling` with and without a warehouse,
  and price on `StockMovement`, but the local Postgres the `moretest` harness needs was down (Docker engine not
  running). Build and vet are clean; the assertions still need a real run.
- `StockMovementRequest` carries no `role_base.v1.request_policy`, unlike most messages in the file. The three
  new requests declare `allow_only_authenticated: true`; the original was left alone to avoid changing the
  behaviour of a live endpoint. Worth deciding deliberately.
