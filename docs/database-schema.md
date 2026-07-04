# Database Schema & Model
Schema and Model that have :
1. Product Config
2. Rack
3. Warehouse Product
4. Inventory Transaction
5. Warehouse

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
