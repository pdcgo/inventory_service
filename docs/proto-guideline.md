# Rule Creating Proto Schema for Rpc that load Data List
This rule for creating schema rpc data list like for example `RackList`, `ProductList` and other.<br>
All api list must be flexible for load various data and maybe various metric of statistic. So we need flexible structure that can cover it.
Base structure of list api rpc must obey of this structure :
```
message ListFilter {

	...
}

enum ListDataType { // this hold what kind data that have loaded in response
	LIST_DATA_TYPE_UNSPECIFIED
	LIST_DATA_TYPE_GENERAL
	LIST_DATA_TYPE_RACK_PRODUCT 		// <-- this is for example
}


enum GeneralSort { 						// <--- this data always paired ListDataType with LIST_DATA_TYPE_GENERAL
	GENERAL_SORT_UNSPECIFIED
	GENERAL_SORT_NAME
}

enum RackProductSort { 					// <--- this data always paired ListDataType with LIST_DATA_TYPE_RACK_PRODUCT
	GENERAL_SORT_UNSPECIFIED
	GENERAL_SORT_PRODUCT_COUNT			// <--- this sort field is reflected with RackProductItem fields. field with name id usually not included if not explicit necessary
	GENERAL_SORT_PRODUCT_AMOUNT
}


message ListFilterSort {
	CommonSortType sort_type
	oneof s {
		GeneralSort 	general
		RackProductSort rack_product
	}
}

message ListRequest {
	ListFilter 				filter
	ListFilterSort 			sort
	repeated ListDataType 	data_request
	CommonPagination 		page
}

message GeneralItem {
	uint64 id
	string name
}
message GeneralMapItem { 					// <--- this data always paired ListDataType with LIST_DATA_TYPE_GENERAL
	map<uint64, GeneralItem> map_data
}

// example various data/metric can be loaded
message RackProductItem {
	uint64 	id
	int64 	product_count
	double 	product_amount
}

message RackProductMapItem { 				// <--- this data always paired ListDataType with LIST_DATA_TYPE_RACK_PRODUCT
	map<uint64, RackProductItem> map_data
}

message ListResponseItem {
	oneof d {
		GeneralMapItem 		general
		RackProductMapItem 	rack_product
	}
}

message ListResponse {
	repeated ListResponseItem 	items
	repeated uint64 			ids 	// <--- this is sorted ids of the data
}

```