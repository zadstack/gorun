-- Full-text search over the plain text of each item (Go writes it with every content write).
-- The trigram tokenizer matches substrings in any script, so Arabic words match with an
-- article or prefix attached, as the LIKE search before it did.
CREATE VIRTUAL TABLE content_fts USING fts5(content_id UNINDEXED, title, excerpt, body, tokenize = 'trigram');
