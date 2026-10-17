ALTER TABLE user_navigation_preferences ADD COLUMN item_order_json TEXT NOT NULL DEFAULT '[]' CHECK(json_valid(item_order_json));
ALTER TABLE user_navigation_preferences ADD COLUMN group_order_json TEXT NOT NULL DEFAULT '[]' CHECK(json_valid(group_order_json));
