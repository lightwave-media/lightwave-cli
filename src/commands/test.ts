import { Command } from "commander";
import chalk from "chalk";
import ora from "ora";
import { mkdir, writeFile, readFile } from "fs/promises";
import { existsSync } from "fs";
import { join } from "path";
import { exec } from "../utils/exec.js";
import {
  findWorkspaceRoot,
  getDomainPath,
  getPackagePath,
} from "../utils/paths.js";

export const testCommand = new Command("test").description(
  "Test scaffolding and running commands",
);

/**
 * lw test:create <domain> <app>/<test_name>
 * Create a new test file with boilerplate
 */
testCommand
  .command("create <domain> <path>")
  .description(
    "Create a test file (e.g., lw test:create cineos.io users/test_permissions)",
  )
  .option("--type <type>", "Test type: unit, integration, e2e", "unit")
  .option("--dry-run", "Preview what would be created")
  .action(async (domain: string, path: string, options) => {
    const domainPath = getDomainPath(domain);
    const [appName, testName] = path.split("/");

    if (!existsSync(domainPath)) {
      console.log(chalk.red(`Domain not found: ${domain}`));
      return;
    }

    const testsDir = join(domainPath, "apps", appName, "tests");
    const testFile = join(testsDir, `${testName}.py`);

    const testContent = generateTestFile(appName, testName, options.type);

    if (options.dryRun) {
      console.log(chalk.blue("\n=== Dry Run: Test File ===\n"));
      console.log(chalk.yellow("Would create:"), testFile);
      console.log(chalk.yellow("\nGenerated code:\n"));
      console.log(testContent);
      return;
    }

    const spinner = ora(`Creating test: ${testName}`).start();

    try {
      await mkdir(testsDir, { recursive: true });

      // Ensure __init__.py exists
      const initPath = join(testsDir, "__init__.py");
      if (!existsSync(initPath)) {
        await writeFile(initPath, "");
      }

      await writeFile(testFile, testContent);
      spinner.succeed(`Created test: ${testName}`);
      console.log(chalk.gray(`  → ${testFile}`));
      console.log(chalk.yellow("\nRun with: make test"));
    } catch (err) {
      spinner.fail(`Failed: ${err}`);
    }
  });

/**
 * lw test:run [domain]
 * Run tests for a domain or all domains
 */
testCommand
  .command("run [domain]")
  .description("Run tests for a domain or all domains")
  .option("--app <app>", "Only test specific app")
  .option("--coverage", "Run with coverage report")
  .option("--verbose", "Verbose output")
  .action(
    async (
      domain?: string,
      options?: { app?: string; coverage?: boolean; verbose?: boolean },
    ) => {
      const root = findWorkspaceRoot();

      if (domain) {
        const domainPath = getDomainPath(domain);
        console.log(chalk.blue(`\n=== Running Tests: ${domain} ===\n`));

        const args = ["test"];
        if (options?.app) {
          args.push(`ARGS=apps.${options.app}`);
        }
        if (options?.coverage) {
          args.push("COVERAGE=1");
        }

        try {
          await exec("make", args, { cwd: domainPath });
        } catch (err) {
          console.log(chalk.red("Tests failed"));
        }
        return;
      }

      // Run tests across all domains
      const { readdir } = await import("fs/promises");
      const domainsDir = join(root, "domains");
      const domains = await readdir(domainsDir);

      console.log(chalk.blue("\n=== Running Tests Across Domains ===\n"));

      let passed = 0;
      let failed = 0;

      for (const d of domains) {
        const domainPath = join(domainsDir, d);
        if (!existsSync(join(domainPath, "Makefile"))) continue;

        const spinner = ora(`${d}`).start();

        try {
          await exec("make", ["test"], { cwd: domainPath, silent: true });
          spinner.succeed(`${d}`);
          passed++;
        } catch {
          spinner.fail(`${d}`);
          failed++;
        }
      }

      console.log(chalk.blue("\n=== Summary ==="));
      console.log(chalk.green(`  Passed: ${passed}`));
      if (failed > 0) {
        console.log(chalk.red(`  Failed: ${failed}`));
      }
    },
  );

/**
 * lw test:coverage [domain]
 * Generate coverage report
 */
testCommand
  .command("coverage [domain]")
  .description("Generate test coverage report")
  .action(async (domain?: string) => {
    if (domain) {
      const domainPath = getDomainPath(domain);
      console.log(chalk.blue(`\n=== Coverage: ${domain} ===\n`));
      await exec("make", ["test", "COVERAGE=1"], { cwd: domainPath });
      return;
    }

    console.log(
      chalk.yellow("Specify a domain for coverage: lw test:coverage cineos.io"),
    );
  });

/**
 * lw test:watch [domain]
 * Run tests in watch mode (if supported)
 */
testCommand
  .command("watch <domain>")
  .description("Run tests in watch mode")
  .option("--app <app>", "Only watch specific app")
  .action(async (domain: string, options) => {
    const domainPath = getDomainPath(domain);

    console.log(chalk.blue(`\n=== Watch Mode: ${domain} ===\n`));
    console.log(chalk.gray("Press Ctrl+C to stop\n"));

    const args = ["test-watch"];
    if (options.app) {
      args.push(`ARGS=apps.${options.app}`);
    }

    try {
      await exec("make", args, { cwd: domainPath });
    } catch {
      console.log(
        chalk.yellow(
          "Watch mode may not be configured. Using standard test run.",
        ),
      );
      await exec("make", ["test"], { cwd: domainPath });
    }
  });

/**
 * lw test:ui
 * Run UI component tests
 */
testCommand
  .command("ui")
  .description("Run lightwave-ui component tests")
  .option("--watch", "Run in watch mode")
  .option("--coverage", "Generate coverage report")
  .action(async (options) => {
    const uiPath = getPackagePath("lightwave-ui");

    console.log(chalk.blue("\n=== UI Component Tests ===\n"));

    const args = ["test"];
    if (options.watch) {
      args.push("--watch");
    }
    if (options.coverage) {
      args.push("--coverage");
    }

    try {
      await exec("pnpm", args, { cwd: uiPath });
    } catch {
      console.log(chalk.red("UI tests failed"));
    }
  });

/**
 * lw test:factory <domain> <app>/<model>
 * Generate a test factory for a model
 */
testCommand
  .command("factory <domain> <path>")
  .description(
    "Generate a test factory (e.g., lw test:factory cineos.io users/User)",
  )
  .option("--dry-run", "Preview what would be created")
  .action(async (domain: string, path: string, options) => {
    const [appName, modelName] = path.split("/");
    const domainPath = getDomainPath(domain);
    const factoriesDir = join(
      domainPath,
      "apps",
      appName,
      "tests",
      "factories",
    );
    const factoryFile = join(
      factoriesDir,
      `${modelName.toLowerCase()}_factory.py`,
    );

    const factoryContent = generateFactory(appName, modelName);

    if (options.dryRun) {
      console.log(chalk.blue("\n=== Dry Run: Factory ===\n"));
      console.log(chalk.yellow("Would create:"), factoryFile);
      console.log(chalk.yellow("\nGenerated code:\n"));
      console.log(factoryContent);
      return;
    }

    const spinner = ora(`Creating factory: ${modelName}Factory`).start();

    try {
      await mkdir(factoriesDir, { recursive: true });

      // Ensure __init__.py exists
      const initPath = join(factoriesDir, "__init__.py");
      if (!existsSync(initPath)) {
        await writeFile(initPath, "");
      }

      await writeFile(factoryFile, factoryContent);
      spinner.succeed(`Created factory: ${modelName}Factory`);
      console.log(chalk.gray(`  → ${factoryFile}`));
    } catch (err) {
      spinner.fail(`Failed: ${err}`);
    }
  });

// Helper functions

function generateTestFile(
  appName: string,
  testName: string,
  testType: string,
): string {
  const className = testName
    .split("_")
    .map((s) => s.charAt(0).toUpperCase() + s.slice(1))
    .join("");

  if (testType === "integration") {
    return `from django.test import TestCase, Client
from django.urls import reverse
from apps.teams.tests.factories import TeamFactory
from apps.users.tests.factories import UserFactory


class ${className}Tests(TestCase):
    """Integration tests for ${appName}."""

    def setUp(self):
        self.client = Client()
        self.user = UserFactory()
        self.team = TeamFactory()
        self.team.members.add(self.user)

    def test_authenticated_access(self):
        """Test that authenticated users can access the resource."""
        self.client.force_login(self.user)
        # TODO: Add test implementation
        self.assertTrue(True)

    def test_unauthenticated_redirect(self):
        """Test that unauthenticated users are redirected."""
        # TODO: Add test implementation
        self.assertTrue(True)
`;
  }

  if (testType === "e2e") {
    return `from django.contrib.staticfiles.testing import StaticLiveServerTestCase
from django.test import tag


@tag("e2e")
class ${className}E2ETests(StaticLiveServerTestCase):
    """End-to-end tests for ${appName}."""

    @classmethod
    def setUpClass(cls):
        super().setUpClass()
        # Setup browser/selenium here if needed

    def test_user_flow(self):
        """Test complete user flow."""
        # TODO: Add e2e test implementation
        self.assertTrue(True)
`;
  }

  // Default: unit test
  return `from django.test import TestCase


class ${className}Tests(TestCase):
    """Unit tests for ${appName}."""

    def setUp(self):
        """Set up test fixtures."""
        pass

    def test_placeholder(self):
        """Placeholder test - replace with real tests."""
        # TODO: Add test implementation
        self.assertTrue(True)

    def test_example_case(self):
        """Example test case."""
        # TODO: Add test implementation
        expected = True
        actual = True
        self.assertEqual(expected, actual)
`;
}

function generateFactory(appName: string, modelName: string): string {
  return `import factory
from factory.django import DjangoModelFactory
from apps.${appName}.models import ${modelName}


class ${modelName}Factory(DjangoModelFactory):
    """Factory for ${modelName} model."""

    class Meta:
        model = ${modelName}

    # Add field definitions here
    # name = factory.Faker("name")
    # email = factory.Faker("email")
    # created_at = factory.LazyFunction(timezone.now)

    @classmethod
    def _create(cls, model_class, *args, **kwargs):
        """Override create to handle any special logic."""
        return super()._create(model_class, *args, **kwargs)
`;
}
