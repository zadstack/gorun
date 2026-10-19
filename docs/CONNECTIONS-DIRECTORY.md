# Connection directory

The Connections screen lists saved named MCP, AI and S3 profiles in one detailed table. It does not synthesize profiles from environment variables or turn an empty installation into a configured one. Legacy Metricool settings appear separately only when stored configuration exists.

The table shows stored names, types, provider/protocol, model or bucket, service host, credential presence, last test result/time, and explicit actions. Optional columns expose storage region, returned-item/protocol metadata and profile IDs/versions. Search and type/result filters operate on the already authorized, bounded profile list; opening, searching and column changes perform no probe or execution.

Only the service host appears in the listing, including search data. Endpoint paths, query parameters, credentials and encrypted values are excluded. Explicitly editing a selected profile retains its full existing endpoint in the original authorized editor, with password fields empty; this avoids silently changing a customer's endpoint. Saved credential presence does not mean validity, and a stored test pass is not live availability.

The name column stays visible in the horizontally scrollable table. Filters and optional column controls are labelled in English and Arabic. Without JavaScript the full table and native edit disclosure remain available. Save, test, activation and removal retain their existing CSRF, permission and version guards; removal remains a separate explicit action.
