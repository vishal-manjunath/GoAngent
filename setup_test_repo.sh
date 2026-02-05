#!/bin/bash
# Creates a test git repo with sample commits for testing git_context extraction

set -e

TEST_REPO_PATH="${1:-/tmp/opscure-test-repo}"

echo "Creating test git repo at: $TEST_REPO_PATH"

# Clean up if exists
rm -rf "$TEST_REPO_PATH"
mkdir -p "$TEST_REPO_PATH"
cd "$TEST_REPO_PATH"

# Initialize repo
git init

# Configure git identity
git config user.name "ops-bot"
git config user.email "ops@company.com"

# Add remote
git remote add origin https://github.com/company/checkout-service.git

# Commit 1: Initial setup
mkdir -p src/main/resources src/main/java/com/checkout
cat > src/main/resources/application.yml << 'EOF'
server:
  port: 8080

database:
  url: jdbc:postgresql://localhost:5432/checkout
  maxPoolSize: 100
  minPoolSize: 10
  connectionTimeout: 30000
EOF

cat > src/main/java/com/checkout/DatabaseConfig.java << 'EOF'
package com.checkout;

public class DatabaseConfig {
    private int maxPoolSize = 100;
    private int minPoolSize = 10;
    
    public int getMaxPoolSize() {
        return maxPoolSize;
    }
}
EOF

git add .
git commit -m "Initial database configuration"

# Commit 2: Add logging
cat > src/main/resources/logback.xml << 'EOF'
<configuration>
  <appender name="STDOUT" class="ch.qos.logback.core.ConsoleAppender">
    <encoder>
      <pattern>%d{HH:mm:ss.SSS} [%thread] %-5level %logger{36} - %msg%n</pattern>
    </encoder>
  </appender>
  <root level="INFO">
    <appender-ref ref="STDOUT" />
  </root>
</configuration>
EOF

git add .
git commit -m "Add logging configuration"

# Commit 3: Update pool settings (this creates the diff we want to test)
cat > src/main/resources/application.yml << 'EOF'
server:
  port: 8080

database:
  url: jdbc:postgresql://localhost:5432/checkout
  maxPoolSize: 20
  minPoolSize: 5
  connectionTimeout: 15000
EOF

cat > src/main/java/com/checkout/DatabaseConfig.java << 'EOF'
package com.checkout;

public class DatabaseConfig {
    private int maxPoolSize = 20;
    private int minPoolSize = 5;
    
    public int getMaxPoolSize() {
        return maxPoolSize;
    }
    
    public int getMinPoolSize() {
        return minPoolSize;
    }
}
EOF

git add .
git commit -m "Update database pool settings"

# Show what we created
echo ""
echo "========================================="
echo "Test repo created successfully!"
echo "========================================="
echo ""
echo "Repo path: $TEST_REPO_PATH"
echo ""
echo "Git config:"
git config user.name
git config user.email
echo ""
echo "Recent commits:"
git log --oneline -n 5
echo ""
echo "Changed files (last commit):"
git diff-tree --no-commit-id --name-only -r HEAD
echo ""
echo "Diff (last commit):"
git show HEAD --format="" --stat