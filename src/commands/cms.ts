import { Command } from "commander";
import chalk from "chalk";
import { readFile, writeFile } from "fs/promises";
import { existsSync } from "fs";
import { join, basename, dirname } from "path";
import { getPackagePath } from "../utils/paths.js";

export const cmsCommand = new Command("cms")
  .description("CMS integration commands - make components props-driven");

interface ExtractedContent {
  sectionTitle?: string;
  headline?: string;
  description?: string;
  features: Array<{
    title: string;
    subtitle: string;
    icon?: string;
    checkItems?: string[];
  }>;
  images: Array<{
    alt: string;
    lightSrc: string;
    darkSrc?: string;
  }>;
}

/**
 * lw cms:analyze <component-path>
 * Analyze a component and extract hardcoded content
 */
cmsCommand
  .command("analyze <path>")
  .description("Analyze a component and show what content could be props")
  .action(async (componentPath: string) => {
    const uiPath = getPackagePath("lightwave-ui");
    const fullPath = componentPath.startsWith("/")
      ? componentPath
      : join(uiPath, "src/components", componentPath);

    if (!existsSync(fullPath)) {
      console.log(chalk.red(`Component not found: ${fullPath}`));
      return;
    }

    const content = await readFile(fullPath, "utf-8");
    const extracted = analyzeComponent(content);

    console.log(chalk.blue("\n=== Component Analysis ===\n"));
    console.log(chalk.yellow("File:"), fullPath);

    console.log(chalk.yellow("\nExtracted Content:"));

    if (extracted.sectionTitle) {
      console.log(chalk.gray("  Section Title:"), extracted.sectionTitle);
    }
    if (extracted.headline) {
      console.log(chalk.gray("  Headline:"), extracted.headline);
    }
    if (extracted.description) {
      console.log(chalk.gray("  Description:"), extracted.description.slice(0, 80) + "...");
    }

    if (extracted.features.length > 0) {
      console.log(chalk.yellow("\nFeatures:"), extracted.features.length);
      extracted.features.forEach((f, i) => {
        console.log(chalk.gray(`  [${i + 1}] ${f.title}`));
        if (f.checkItems?.length) {
          console.log(chalk.gray(`      Check items: ${f.checkItems.length}`));
        }
      });
    }

    if (extracted.images.length > 0) {
      console.log(chalk.yellow("\nImages:"), extracted.images.length);
      extracted.images.forEach((img, i) => {
        console.log(chalk.gray(`  [${i + 1}] ${img.alt || "(no alt)"}`));
      });
    }

    // Generate suggested props interface
    console.log(chalk.yellow("\n=== Suggested Props Interface ===\n"));
    console.log(generatePropsInterface(basename(fullPath, ".tsx"), extracted));
  });

/**
 * lw cms:convert <component-path>
 * Convert a hardcoded component to props-driven
 */
cmsCommand
  .command("convert <path>")
  .description("Convert a hardcoded component to be props-driven")
  .option("--dry-run", "Preview changes without writing")
  .option("--output <file>", "Write to a different file")
  .action(async (componentPath: string, options) => {
    const uiPath = getPackagePath("lightwave-ui");
    const fullPath = componentPath.startsWith("/")
      ? componentPath
      : join(uiPath, "src/components", componentPath);

    if (!existsSync(fullPath)) {
      console.log(chalk.red(`Component not found: ${fullPath}`));
      return;
    }

    const content = await readFile(fullPath, "utf-8");
    const componentName = basename(fullPath, ".tsx");
    const pascalName = kebabToPascal(componentName);

    const extracted = analyzeComponent(content);
    const propsInterface = generatePropsInterface(componentName, extracted);
    const convertedComponent = generateCMSComponent(pascalName, extracted, content);

    if (options.dryRun) {
      console.log(chalk.blue("\n=== Dry Run: Converted Component ===\n"));
      console.log(propsInterface);
      console.log("\n// Component would use these props...\n");
      console.log(chalk.gray("(Full conversion requires manual review)"));

      console.log(chalk.yellow("\nDefault content export:"));
      console.log(generateDefaultContent(pascalName, extracted));
      return;
    }

    const outputPath = options.output || fullPath.replace(".tsx", "-cms.tsx");
    const fullOutput = propsInterface + "\n\n" + convertedComponent;

    await writeFile(outputPath, fullOutput);
    console.log(chalk.green(`✓ Converted component written to: ${outputPath}`));

    // Also write default content
    const defaultContentPath = outputPath.replace(".tsx", ".content.ts");
    await writeFile(defaultContentPath, generateDefaultContent(pascalName, extracted));
    console.log(chalk.green(`✓ Default content written to: ${defaultContentPath}`));
  });

/**
 * lw cms:list
 * List components that need CMS conversion
 */
cmsCommand
  .command("list [category]")
  .description("List components with hardcoded content")
  .action(async (category?: string) => {
    const uiPath = getPackagePath("lightwave-ui");
    const { readdir } = await import("fs/promises");

    const searchPath = category
      ? join(uiPath, "src/components", category)
      : join(uiPath, "src/components/marketing");

    console.log(chalk.blue("\n=== Components with Hardcoded Content ===\n"));

    const categories = category ? [category] : await readdir(searchPath);

    for (const cat of categories) {
      const catPath = join(searchPath, cat);
      if (!existsSync(catPath)) continue;

      const files = await readdir(catPath);
      const tsxFiles = files.filter(f => f.endsWith(".tsx") && !f.includes(".content."));

      for (const file of tsxFiles) {
        const filePath = join(catPath, file);
        const content = await readFile(filePath, "utf-8");

        // Check if component has hardcoded strings
        const hasHardcodedContent = hasHardcodedStrings(content);
        const hasProps = content.includes("Props {") || content.includes("Props{");

        if (hasHardcodedContent && !hasProps) {
          console.log(chalk.yellow(`  ${cat}/${file}`), chalk.gray("- needs conversion"));
        }
      }
    }
  });

// Helper functions

function analyzeComponent(content: string): ExtractedContent {
  const result: ExtractedContent = {
    features: [],
    images: [],
  };

  // Extract section title (usually in a span with "Features" etc)
  const sectionMatch = content.match(/<span[^>]*>([^<]+)<\/span>/);
  if (sectionMatch) {
    result.sectionTitle = sectionMatch[1].trim();
  }

  // Extract headline (h2 tags)
  const headlineMatch = content.match(/<h2[^>]*>([^<]+)<\/h2>/);
  if (headlineMatch) {
    result.headline = headlineMatch[1].trim();
  }

  // Extract description (p tags after headline)
  const descMatches = content.match(/<p[^>]*className="[^"]*text-tertiary[^"]*"[^>]*>\s*([^<]+)/g);
  if (descMatches && descMatches.length > 0) {
    const match = descMatches[0].match(/>([^<]+)/);
    if (match) {
      result.description = match[1].trim();
    }
  }

  // Extract features from array literals
  const featureArrayMatch = content.match(/\[\s*\{[\s\S]*?title:[\s\S]*?\}\s*\]/g);
  if (featureArrayMatch) {
    for (const arrayStr of featureArrayMatch) {
      const titleMatches = arrayStr.matchAll(/title:\s*["']([^"']+)["']/g);
      const subtitleMatches = arrayStr.matchAll(/subtitle:\s*["']([^"']+)["']/g);

      const titles = [...titleMatches].map(m => m[1]);
      const subtitles = [...subtitleMatches].map(m => m[1]);

      titles.forEach((title, i) => {
        result.features.push({
          title,
          subtitle: subtitles[i] || "",
        });
      });
    }
  }

  // Extract features from inline h2 tags within grid
  const h2Matches = content.matchAll(/<h2[^>]*>([^<]+)<\/h2>[\s\S]*?<p[^>]*>([^<]+)<\/p>/g);
  for (const match of h2Matches) {
    if (!result.features.find(f => f.title === match[1].trim())) {
      result.features.push({
        title: match[1].trim(),
        subtitle: match[2].trim(),
      });
    }
  }

  // Extract check items
  const checkItemsMatch = content.match(/\.map\(\(feat\)[\s\S]*?\[([^\]]+)\]/);
  if (checkItemsMatch) {
    const items = checkItemsMatch[1].matchAll(/["']([^"']+)["']/g);
    const checkItems = [...items].map(m => m[1]);
    if (result.features.length > 0 && checkItems.length > 0) {
      result.features[result.features.length - 1].checkItems = checkItems;
    }
  }

  // Extract images
  const imgMatches = content.matchAll(/<img[^>]*alt=["']([^"']*)["'][^>]*src=["']([^"']+)["']/g);
  for (const match of imgMatches) {
    result.images.push({
      alt: match[1],
      lightSrc: match[2],
    });
  }

  return result;
}

function hasHardcodedStrings(content: string): boolean {
  // Check for hardcoded text in JSX
  const patterns = [
    />\s*[A-Z][a-z]+.*?</,  // Text content starting with capital
    /title:\s*["'][^"']{10,}["']/,  // Long title strings
    /subtitle:\s*["'][^"']{20,}["']/,  // Long subtitle strings
  ];

  return patterns.some(p => p.test(content));
}

function kebabToPascal(kebab: string): string {
  return kebab
    .split("-")
    .map(s => s.charAt(0).toUpperCase() + s.slice(1))
    .join("");
}

function generatePropsInterface(componentName: string, extracted: ExtractedContent): string {
  const pascalName = kebabToPascal(componentName);

  let props = `export interface ${pascalName}Props {\n`;

  if (extracted.sectionTitle) {
    props += `  /** Section label (e.g., "Features") */\n  sectionTitle?: string;\n`;
  }
  if (extracted.headline) {
    props += `  /** Main headline */\n  headline?: string;\n`;
  }
  if (extracted.description) {
    props += `  /** Section description */\n  description?: string;\n`;
  }

  if (extracted.features.length > 0) {
    props += `  /** Feature items */\n  features?: Array<{\n`;
    props += `    title: string;\n`;
    props += `    subtitle: string;\n`;
    props += `    icon?: React.ComponentType<{ className?: string }>;\n`;
    if (extracted.features.some(f => f.checkItems?.length)) {
      props += `    checkItems?: string[];\n`;
    }
    props += `  }>;\n`;
  }

  if (extracted.images.length > 0) {
    props += `  /** Images */\n  images?: Array<{\n`;
    props += `    alt: string;\n`;
    props += `    lightSrc: string;\n`;
    props += `    darkSrc?: string;\n`;
    props += `  }>;\n`;
  }

  props += `}`;

  return props;
}

function generateCMSComponent(pascalName: string, extracted: ExtractedContent, originalContent: string): string {
  // This is a simplified version - full conversion requires manual work
  return `export function ${pascalName}(props: ${pascalName}Props) {
  const {
    sectionTitle = "${extracted.sectionTitle || "Features"}",
    headline = "${extracted.headline || ""}",
    description = "${extracted.description?.slice(0, 50) || ""}...",
    features = defaultFeatures,
    images = defaultImages,
  } = props;

  // TODO: Replace hardcoded content in original component with props
  // Original component structure preserved below for reference

  return (
    // ... converted JSX using props
  );
}`;
}

function generateDefaultContent(pascalName: string, extracted: ExtractedContent): string {
  let content = `// Default content for ${pascalName}\n`;
  content += `// Import this and spread into component, or use CMS data\n\n`;

  content += `export const default${pascalName}Content = {\n`;

  if (extracted.sectionTitle) {
    content += `  sectionTitle: "${extracted.sectionTitle}",\n`;
  }
  if (extracted.headline) {
    content += `  headline: "${extracted.headline}",\n`;
  }
  if (extracted.description) {
    content += `  description: "${extracted.description.replace(/"/g, '\\"')}",\n`;
  }

  if (extracted.features.length > 0) {
    content += `  features: [\n`;
    for (const f of extracted.features) {
      content += `    {\n`;
      content += `      title: "${f.title}",\n`;
      content += `      subtitle: "${f.subtitle.replace(/"/g, '\\"')}",\n`;
      if (f.checkItems?.length) {
        content += `      checkItems: [\n`;
        for (const item of f.checkItems) {
          content += `        "${item}",\n`;
        }
        content += `      ],\n`;
      }
      content += `    },\n`;
    }
    content += `  ],\n`;
  }

  if (extracted.images.length > 0) {
    content += `  images: [\n`;
    for (const img of extracted.images) {
      content += `    {\n`;
      content += `      alt: "${img.alt}",\n`;
      content += `      lightSrc: "${img.lightSrc}",\n`;
      if (img.darkSrc) {
        content += `      darkSrc: "${img.darkSrc}",\n`;
      }
      content += `    },\n`;
    }
    content += `  ],\n`;
  }

  content += `};\n`;

  return content;
}
