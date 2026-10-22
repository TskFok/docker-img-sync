-- 表名不带前缀。若 MYSQL_PREFIX 非空，建表时加上同一个前缀。

CREATE TABLE `credential` (
  `id` int NOT NULL AUTO_INCREMENT,
  `name` varchar(255) NOT NULL,
  `username` varchar(255) NOT NULL,
  `password_encrypted` text NOT NULL,
  `created_at` datetime NOT NULL,
  `updated_at` datetime NOT NULL,
  PRIMARY KEY (`id`),
  UNIQUE KEY `uk_credential_name` (`name`)
) ENGINE=InnoDB DEFAULT CHARSET=utf8mb4 COLLATE=utf8mb4_general_ci;

CREATE TABLE `registry` (
  `id` int NOT NULL AUTO_INCREMENT,
  `name` varchar(255) NOT NULL,
  `address` varchar(255) NOT NULL,
  `namespace` varchar(255) NOT NULL,
  `credential_id` int NOT NULL,
  `created_at` datetime NOT NULL,
  `updated_at` datetime NOT NULL,
  PRIMARY KEY (`id`),
  UNIQUE KEY `uk_registry_name` (`name`),
  CONSTRAINT `fk_registry_credential` FOREIGN KEY (`credential_id`) REFERENCES `credential` (`id`) ON DELETE RESTRICT
) ENGINE=InnoDB DEFAULT CHARSET=utf8mb4 COLLATE=utf8mb4_general_ci;

CREATE TABLE `sync_task` (
  `id` int NOT NULL AUTO_INCREMENT,
  `name` varchar(255) NOT NULL,
  `source_image` varchar(512) NOT NULL,
  `source_credential_id` int NULL,
  `registry_id` int NOT NULL,
  `dest_repository` varchar(255) NOT NULL DEFAULT '',
  `dest_tag` varchar(255) NOT NULL DEFAULT '',
  `interval_seconds` int NOT NULL DEFAULT 0,
  `enabled` tinyint NOT NULL DEFAULT 1,
  `last_digest` varchar(255) NOT NULL DEFAULT '',
  `last_status` varchar(32) NOT NULL DEFAULT 'idle',
  `progress_phase` varchar(16) NOT NULL DEFAULT '',
  `last_error` text NOT NULL,
  `last_synced_at` datetime NULL,
  `next_run_at` datetime NULL,
  `created_at` datetime NOT NULL,
  `updated_at` datetime NOT NULL,
  PRIMARY KEY (`id`),
  KEY `idx_sync_task_due` (`enabled`, `next_run_at`),
  CONSTRAINT `fk_sync_task_registry` FOREIGN KEY (`registry_id`) REFERENCES `registry` (`id`) ON DELETE RESTRICT,
  CONSTRAINT `fk_sync_task_source_credential` FOREIGN KEY (`source_credential_id`) REFERENCES `credential` (`id`) ON DELETE RESTRICT
) ENGINE=InnoDB DEFAULT CHARSET=utf8mb4 COLLATE=utf8mb4_general_ci;

CREATE TABLE `sync_log` (
  `id` int NOT NULL AUTO_INCREMENT,
  `sync_task_id` int NOT NULL,
  `trigger` varchar(16) NOT NULL,
  `status` varchar(16) NOT NULL,
  `source_digest` varchar(255) NOT NULL DEFAULT '',
  `message` text NOT NULL,
  `started_at` datetime NOT NULL,
  `finished_at` datetime NOT NULL,
  PRIMARY KEY (`id`),
  CONSTRAINT `fk_sync_log_task` FOREIGN KEY (`sync_task_id`) REFERENCES `sync_task` (`id`) ON DELETE CASCADE
) ENGINE=InnoDB DEFAULT CHARSET=utf8mb4 COLLATE=utf8mb4_general_ci;
