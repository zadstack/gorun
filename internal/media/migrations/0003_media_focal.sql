-- Optional focal point of an image, each coordinate from 0 (left/top) to 1
-- (right/bottom). Themes keep it in view when they crop an image to a frame
-- ({{img}} prints it as object-position). NULL means no focal point.
ALTER TABLE media ADD COLUMN focal_x REAL;
ALTER TABLE media ADD COLUMN focal_y REAL;
