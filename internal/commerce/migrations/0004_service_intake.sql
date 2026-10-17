ALTER TABLE products ADD COLUMN intake_json TEXT NOT NULL DEFAULT '[]' CHECK(json_valid(intake_json));
