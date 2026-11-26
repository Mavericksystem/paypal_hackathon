ALTER TABLE intents
    ADD COLUMN item TEXT NOT NULL DEFAULT '',
    ADD COLUMN quantity INTEGER NOT NULL DEFAULT 0,
    ADD COLUMN seller_text TEXT NOT NULL DEFAULT '',
    ADD CONSTRAINT intents_quantity_valid CHECK (quantity >= 0),
    ADD CONSTRAINT intents_item_len CHECK (char_length(item) <= 200),
    ADD CONSTRAINT intents_seller_text_len CHECK (char_length(seller_text) <= 8000);