import { Command } from "commander";
import chalk from "chalk";
import ora from "ora";
import { mkdir, writeFile, readFile } from "fs/promises";
import { existsSync } from "fs";
import { join } from "path";
import { exec } from "../utils/exec.js";
import { findWorkspaceRoot, getDomainPath } from "../utils/paths.js";

export const djangoCommand = new Command("django")
  .alias("dj")
  .description("Django scaffolding commands");

/**
 * lw django:app <domain> <app_name>
 * Create a new Django app with standard structure
 */
djangoCommand
  .command("app <domain> <app_name>")
  .description("Create a new Django app (e.g., lw django:app cineos.io analytics)")
  .option("--with-api", "Include DRF serializers and viewsets")
  .option("--with-admin", "Include admin configuration")
  .option("--dry-run", "Preview what would be created")
  .action(async (domain: string, appName: string, options) => {
    const domainPath = getDomainPath(domain);

    if (!existsSync(domainPath)) {
      console.log(chalk.red(`Domain not found: ${domain}`));
      return;
    }

    const appPath = join(domainPath, "apps", appName);

    if (options.dryRun) {
      console.log(chalk.blue("\n=== Dry Run: Django App ===\n"));
      console.log(chalk.yellow("Would create:"));
      console.log(chalk.gray(`  ${appPath}/`));
      console.log(chalk.gray(`  ${appPath}/__init__.py`));
      console.log(chalk.gray(`  ${appPath}/models.py`));
      console.log(chalk.gray(`  ${appPath}/views.py`));
      console.log(chalk.gray(`  ${appPath}/urls.py`));
      console.log(chalk.gray(`  ${appPath}/forms.py`));
      if (options.withAdmin) {
        console.log(chalk.gray(`  ${appPath}/admin.py`));
      }
      if (options.withApi) {
        console.log(chalk.gray(`  ${appPath}/serializers.py`));
        console.log(chalk.gray(`  ${appPath}/api_views.py`));
        console.log(chalk.gray(`  ${appPath}/api_urls.py`));
      }
      console.log(chalk.gray(`  ${appPath}/tests/`));
      console.log(chalk.gray(`  ${appPath}/tests/__init__.py`));
      console.log(chalk.gray(`  ${appPath}/tests/test_models.py`));
      console.log(chalk.gray(`  ${appPath}/tests/test_views.py`));

      console.log(chalk.yellow("\nGenerated models.py:"));
      console.log(generateModelsFile(appName));
      return;
    }

    const spinner = ora(`Creating Django app: ${appName}`).start();

    try {
      // Create directories
      await mkdir(appPath, { recursive: true });
      await mkdir(join(appPath, "tests"), { recursive: true });
      await mkdir(join(appPath, "templates", appName), { recursive: true });

      // Create files
      await writeFile(join(appPath, "__init__.py"), "");
      await writeFile(join(appPath, "models.py"), generateModelsFile(appName));
      await writeFile(join(appPath, "views.py"), generateViewsFile(appName));
      await writeFile(join(appPath, "urls.py"), generateUrlsFile(appName));
      await writeFile(join(appPath, "forms.py"), generateFormsFile(appName));

      if (options.withAdmin) {
        await writeFile(join(appPath, "admin.py"), generateAdminFile(appName));
      }

      if (options.withApi) {
        await writeFile(join(appPath, "serializers.py"), generateSerializersFile(appName));
        await writeFile(join(appPath, "api_views.py"), generateApiViewsFile(appName));
        await writeFile(join(appPath, "api_urls.py"), generateApiUrlsFile(appName));
      }

      // Test files
      await writeFile(join(appPath, "tests", "__init__.py"), "");
      await writeFile(join(appPath, "tests", "test_models.py"), generateTestModelsFile(appName));
      await writeFile(join(appPath, "tests", "test_views.py"), generateTestViewsFile(appName));

      spinner.succeed(`Created Django app: ${appName}`);
      console.log(chalk.gray(`  → ${appPath}/`));

      console.log(chalk.yellow("\nNext steps:"));
      console.log(chalk.gray(`  1. Add 'apps.${appName}' to INSTALLED_APPS`));
      console.log(chalk.gray(`  2. Include urls in project urls.py`));
      console.log(chalk.gray(`  3. Run: make migrations && make migrate`));
    } catch (err) {
      spinner.fail(`Failed: ${err}`);
    }
  });

/**
 * lw django:model <domain> <app>/<model>
 * Add a model to an existing app
 */
djangoCommand
  .command("model <domain> <path>")
  .description("Add a model to an app (e.g., lw django:model cineos.io analytics/PageView)")
  .option("--fields <fields>", "Comma-separated fields (e.g., name:str,count:int,team:fk)")
  .option("--team-model", "Extend BaseTeamModel instead of BaseModel")
  .option("--dry-run", "Preview what would be generated")
  .action(async (domain: string, path: string, options) => {
    const [appName, modelName] = path.split("/");
    const domainPath = getDomainPath(domain);
    const modelsPath = join(domainPath, "apps", appName, "models.py");

    if (!existsSync(modelsPath)) {
      console.log(chalk.red(`App not found: ${appName} in ${domain}`));
      return;
    }

    const fields = parseFields(options.fields || "");
    const modelCode = generateModelClass(modelName, fields, options.teamModel);

    if (options.dryRun) {
      console.log(chalk.blue("\n=== Dry Run: Model ===\n"));
      console.log(chalk.yellow("Would append to:"), modelsPath);
      console.log(chalk.yellow("\nGenerated code:\n"));
      console.log(modelCode);
      return;
    }

    const spinner = ora(`Adding model: ${modelName}`).start();

    try {
      const existing = await readFile(modelsPath, "utf-8");
      await writeFile(modelsPath, existing + "\n\n" + modelCode);
      spinner.succeed(`Added model: ${modelName}`);
      console.log(chalk.gray(`  → ${modelsPath}`));
      console.log(chalk.yellow("\nNext: make migrations && make migrate"));
    } catch (err) {
      spinner.fail(`Failed: ${err}`);
    }
  });

/**
 * lw django:island <domain> <component_name>
 * Create a React island component for Django
 */
djangoCommand
  .command("island <domain> <name>")
  .description("Create a React island component (e.g., lw django:island cineos.io UserDashboard)")
  .option("--props <props>", "Comma-separated props")
  .option("--dry-run", "Preview what would be created")
  .action(async (domain: string, name: string, options) => {
    const domainPath = getDomainPath(domain);
    const kebabName = pascalToKebab(name);
    const islandPath = join(domainPath, "assets", "islands", `${kebabName}.tsx`);

    const props = options.props
      ? options.props.split(",").map((p: string) => {
          const [propName, propType = "string"] = p.trim().split(":");
          return { name: propName, type: propType };
        })
      : [];

    const islandCode = generateIslandComponent(name, props);
    const templateCode = generateIslandTemplate(name, kebabName, props);

    if (options.dryRun) {
      console.log(chalk.blue("\n=== Dry Run: React Island ===\n"));
      console.log(chalk.yellow("Would create:"), islandPath);
      console.log(chalk.yellow("\nIsland component:\n"));
      console.log(islandCode);
      console.log(chalk.yellow("\nDjango template usage:\n"));
      console.log(templateCode);
      return;
    }

    const spinner = ora(`Creating island: ${name}`).start();

    try {
      await mkdir(join(domainPath, "assets", "islands"), { recursive: true });
      await writeFile(islandPath, islandCode);
      spinner.succeed(`Created island: ${name}`);
      console.log(chalk.gray(`  → ${islandPath}`));
      console.log(chalk.yellow("\nDjango template usage:"));
      console.log(chalk.gray(templateCode));
    } catch (err) {
      spinner.fail(`Failed: ${err}`);
    }
  });

/**
 * lw django:migrations <domain>
 * Check migration status
 */
djangoCommand
  .command("migrations [domain]")
  .description("Check migration status across domains")
  .action(async (domain?: string) => {
    const root = findWorkspaceRoot();

    if (domain) {
      const domainPath = getDomainPath(domain);
      console.log(chalk.blue(`\n=== Migrations: ${domain} ===\n`));
      await exec("make", ["manage", "ARGS=showmigrations"], { cwd: domainPath });
      return;
    }

    // Check all domains
    const { readdir } = await import("fs/promises");
    const domainsDir = join(root, "domains");
    const domains = await readdir(domainsDir);

    for (const d of domains) {
      const domainPath = join(domainsDir, d);
      if (!existsSync(join(domainPath, "Makefile"))) continue;

      console.log(chalk.blue(`\n=== ${d} ===`));
      try {
        const result = await exec("docker", ["compose", "exec", "-T", "web", "python", "manage.py", "showmigrations", "--list"], {
          cwd: domainPath,
          silent: true,
        });
        // Count unapplied migrations
        const unapplied = (result.stdout.match(/\[ \]/g) || []).length;
        if (unapplied > 0) {
          console.log(chalk.yellow(`  ${unapplied} unapplied migrations`));
        } else {
          console.log(chalk.green("  All migrations applied"));
        }
      } catch {
        console.log(chalk.gray("  (containers not running)"));
      }
    }
  });

// Helper functions

interface Field {
  name: string;
  type: string;
}

function parseFields(fieldsStr: string): Field[] {
  if (!fieldsStr) return [];
  return fieldsStr.split(",").map((f) => {
    const [name, type = "str"] = f.trim().split(":");
    return { name, type };
  });
}

function fieldToType(type: string): string {
  const typeMap: Record<string, string> = {
    str: "models.CharField(max_length=255)",
    text: "models.TextField()",
    int: "models.IntegerField()",
    bool: "models.BooleanField(default=False)",
    date: "models.DateField()",
    datetime: "models.DateTimeField()",
    decimal: "models.DecimalField(max_digits=10, decimal_places=2)",
    fk: "models.ForeignKey()",
    json: "models.JSONField(default=dict)",
    url: "models.URLField()",
    email: "models.EmailField()",
  };
  return typeMap[type] || "models.CharField(max_length=255)";
}

function pascalToKebab(pascal: string): string {
  return pascal
    .replace(/([A-Z])/g, "-$1")
    .toLowerCase()
    .replace(/^-/, "");
}

function generateModelsFile(appName: string): string {
  return `from django.db import models
from apps.utils.models import BaseModel


# Create your models here
`;
}

function generateViewsFile(appName: string): string {
  return `from django.shortcuts import render, get_object_or_404
from django.contrib.auth.decorators import login_required


# Create your views here
`;
}

function generateUrlsFile(appName: string): string {
  return `from django.urls import path
from . import views

app_name = "${appName}"

urlpatterns = [
    # Add your URL patterns here
]

team_urlpatterns = [
    # Team-scoped URL patterns
]
`;
}

function generateFormsFile(appName: string): string {
  return `from django import forms


# Create your forms here
`;
}

function generateAdminFile(appName: string): string {
  return `from django.contrib import admin


# Register your models here
`;
}

function generateSerializersFile(appName: string): string {
  return `from rest_framework import serializers


# Create your serializers here
`;
}

function generateApiViewsFile(appName: string): string {
  return `from rest_framework import viewsets, permissions


# Create your API viewsets here
`;
}

function generateApiUrlsFile(appName: string): string {
  return `from django.urls import path, include
from rest_framework.routers import DefaultRouter
from . import api_views

router = DefaultRouter()
# router.register(r'items', api_views.ItemViewSet)

urlpatterns = [
    path("", include(router.urls)),
]
`;
}

function generateTestModelsFile(appName: string): string {
  return `from django.test import TestCase


class ${appName.charAt(0).toUpperCase() + appName.slice(1)}ModelTests(TestCase):
    def test_placeholder(self):
        """Placeholder test - replace with real tests"""
        self.assertTrue(True)
`;
}

function generateTestViewsFile(appName: string): string {
  return `from django.test import TestCase, Client
from django.urls import reverse


class ${appName.charAt(0).toUpperCase() + appName.slice(1)}ViewTests(TestCase):
    def setUp(self):
        self.client = Client()

    def test_placeholder(self):
        """Placeholder test - replace with real tests"""
        self.assertTrue(True)
`;
}

function generateModelClass(name: string, fields: Field[], isTeamModel: boolean): string {
  const baseClass = isTeamModel ? "BaseTeamModel" : "BaseModel";
  const baseImport = isTeamModel
    ? "from apps.teams.models import BaseTeamModel"
    : "from apps.utils.models import BaseModel";

  let code = `\nclass ${name}(${baseClass}):\n`;
  code += `    """${name} model."""\n\n`;

  if (fields.length === 0) {
    code += `    # Add fields here\n`;
    code += `    pass\n`;
  } else {
    for (const field of fields) {
      code += `    ${field.name} = ${fieldToType(field.type)}\n`;
    }
  }

  code += `\n    class Meta:\n`;
  code += `        verbose_name = "${name}"\n`;
  code += `        verbose_name_plural = "${name}s"\n`;

  code += `\n    def __str__(self):\n`;
  code += `        return f"${name} {self.id}"\n`;

  return code;
}

function generateIslandComponent(name: string, props: Array<{ name: string; type: string }>): string {
  const propsInterface = props.length
    ? props.map((p) => `  ${p.name}?: ${p.type};`).join("\n")
    : "  // Add props here";

  return `import { useState } from "react";

export interface ${name}Props {
${propsInterface}
}

export function ${name}(props: ${name}Props) {
  const { ${props.map((p) => p.name).join(", ")} } = props;

  return (
    <div className="${pascalToKebab(name)}">
      {/* Island content */}
    </div>
  );
}

// Island registration
export default ${name};
`;
}

function generateIslandTemplate(name: string, kebabName: string, props: Array<{ name: string; type: string }>): string {
  const propsJson = props.length
    ? props.map((p) => `"${p.name}": "{{ ${p.name} }}"`).join(", ")
    : "";

  return `{% load islands %}

{% island "${kebabName}" ${propsJson ? `props='{ ${propsJson} }'` : ""} %}
{% endisland %}`;
}
