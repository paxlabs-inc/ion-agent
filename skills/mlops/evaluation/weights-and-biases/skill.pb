meta {
  name: "weights-and-biases"
  version: "1.0.0"
  summary: "W&B: log ML experiments, sweeps, model registry, dashboards"
  author: "Orchestra Research"
  license: "MIT"
  platforms: "linux"
  platforms: "macos"
  platforms: "windows"
}

triggers {
  keywords: "wandb"
  keywords: "weights and biases"
  keywords: "experiment tracking"
  keywords: "hyperparameter sweep"
  keywords: "model registry"
  keywords: "training dashboard"
  keywords: "wandb logging"
  intents: "track_experiment"
  intents: "run_sweep"
  intents: "log_metrics"
  intents: "compare_runs"
  intents: "manage_models"
  patterns: "(track|log|monitor) .*(experiment|training|metrics|loss)"
  patterns: "(wandb|weights.and.biases)"
  patterns: "(hyperparameter|hp) .*(sweep|tuning|search)"
  patterns: "model (registry|versioning|lineage)"
}

requires {
  env_optional: "WANDB_API_KEY"
  tools {
    name: "terminal"
    required: true
  }
  binaries: "pip"
  binaries: "python3"
}

provides {
  capabilities: "experiment_tracking"
  capabilities: "hyperparameter_sweeps"
  capabilities: "model_registry"
  capabilities: "metric_visualization"
  capabilities: "artifact_tracking"
}

actions {
  id: "setup"
  description: "Install and authenticate W&B"
  trigger_phrases: "set up wandb"
  trigger_phrases: "install weights and biases"
  trigger_phrases: "configure wandb"
    rules {
      text: "pip install wandb then wandb login (or set WANDB_API_KEY env var)"
      priority: CRITICAL
    }
    rules {
      text: "Free tier: unlimited public projects, 100GB storage"
      priority: HIGH
    }
    rules {
      text: "Academic: free for students/researchers"
      priority: NORMAL
    }
    data {
      key: "install_command"
      string_value: "pip install wandb"
    }
    data {
      key: "auth_command"
      string_value: "wandb login"
    }
}
actions {
  id: "basic_tracking"
  description: "Initialize a W&B run and log training metrics"
  trigger_phrases: "start tracking"
  trigger_phrases: "log training run"
  trigger_phrases: "initialize wandb run"
  trigger_phrases: "track this experiment"
    rules {
      text: "Always call wandb.finish() at the end of training"
      priority: CRITICAL
    }
    rules {
      text: "Pass config dict with hyperparameters to wandb.init() for automatic tracking"
      priority: HIGH
    }
    rules {
      text: "Use hierarchical keys: train/loss, val/accuracy for organized dashboards"
      priority: HIGH
    }
    rules {
      text: "Use descriptive run names like bert-base-lr0.001-bs32-epoch10"
      priority: NORMAL
    }
    examples {
      label: "basic experiment tracking"
      language: "python"
      code: "import wandb\nrun = wandb.init(\n    project=\"my-project\",\n    config={\"learning_rate\": 0.001, \"epochs\": 10, \"batch_size\": 32}\n)\nfor epoch in range(run.config.epochs):\n    train_loss = train_epoch()\n    wandb.log({\"epoch\": epoch, \"train/loss\": train_loss, \"val/loss\": val_loss})\nwandb.finish()"
    }
}
actions {
  id: "pytorch_tracking"
  description: "W&B integration with PyTorch training loops"
  trigger_phrases: "wandb pytorch"
  trigger_phrases: "track pytorch training"
    rules {
      text: "Access config via wandb.config inside training loop"
      priority: HIGH
    }
    rules {
      text: "Log every N batches to avoid overhead: if batch_idx % 100 == 0"
      priority: NORMAL
    }
    examples {
      label: "pytorch integration"
      language: "python"
      code: "import wandb\nwandb.init(project=\"pytorch-demo\", config={\"lr\": 0.001, \"epochs\": 10})\nconfig = wandb.config\nfor epoch in range(config.epochs):\n    for batch_idx, (data, target) in enumerate(train_loader):\n        loss = train_step(data, target)\n        if batch_idx % 100 == 0:\n            wandb.log({\"loss\": loss.item(), \"epoch\": epoch})\nwandb.finish()"
    }
}
actions {
  id: "hyperparameter_sweeps"
  description: "Automated hyperparameter optimization with W&B sweeps"
  trigger_phrases: "hyperparameter sweep"
  trigger_phrases: "hp tuning"
  trigger_phrases: "sweep config"
  trigger_phrases: "optimize hyperparameters"
    rules {
      text: "Three sweep methods: bayes (recommended), grid, random"
      priority: CRITICAL
    }
    rules {
      text: "Define metric with goal (maximize/minimize) in sweep config"
      priority: HIGH
    }
    rules {
      text: "Use wandb.agent(sweep_id, function=train, count=N) to run trials"
      priority: HIGH
    }
    rules {
      text: "Bayesian optimization works best with continuous parameters"
      priority: NORMAL
    }
    data {
      key: "sweep_methods"
      list_value {
        items {
          string_value: "bayes: Bayesian optimization (recommended)"
        }
        items {
          string_value: "grid: exhaustive search"
        }
        items {
          string_value: "random: random sampling"
        }
      }
    }
    examples {
      label: "bayesian sweep setup"
      language: "python"
      code: "sweep_config = {\n    'method': 'bayes',\n    'metric': {'name': 'val/accuracy', 'goal': 'maximize'},\n    'parameters': {\n        'learning_rate': {'distribution': 'log_uniform', 'min': 1e-5, 'max': 1e-1},\n        'batch_size': {'values': [16, 32, 64, 128]},\n    }\n}\nsweep_id = wandb.sweep(sweep_config, project=\"my-project\")\nwandb.agent(sweep_id, function=train, count=50)"
    }
}
actions {
  id: "artifacts_and_registry"
  description: "Track datasets, models, and artifacts with lineage"
  trigger_phrases: "log artifact"
  trigger_phrases: "model registry"
  trigger_phrases: "version model"
  trigger_phrases: "track dataset"
    rules {
      text: "Use wandb.Artifact for models, datasets, and files"
      priority: HIGH
    }
    rules {
      text: "Link artifacts to model registry: run.link_artifact(artifact, 'model-registry/production')"
      priority: HIGH
    }
    rules {
      text: "Add aliases like 'best', 'production' for version management"
      priority: NORMAL
    }
    examples {
      label: "log model artifact"
      language: "python"
      code: "artifact = wandb.Artifact('model', type='model', metadata={'accuracy': 0.95})\nartifact.add_file('model.pth')\nwandb.log_artifact(artifact, aliases=['best', 'production'])"
    }
}
actions {
  id: "framework_integrations"
  description: "Integrate W&B with HuggingFace, PyTorch Lightning, Keras"
  trigger_phrases: "wandb huggingface"
  trigger_phrases: "wandb lightning"
  trigger_phrases: "wandb keras"
  trigger_phrases: "integrate wandb"
    rules {
      text: "HuggingFace Trainer: set report_to='wandb' in TrainingArguments"
      priority: HIGH
    }
    rules {
      text: "PyTorch Lightning: use WandbLogger with log_model=True"
      priority: HIGH
    }
    rules {
      text: "Keras: add WandbCallback() to model.fit callbacks"
      priority: NORMAL
    }
    examples {
      label: "huggingface integration"
      language: "python"
      code: "from transformers import Trainer, TrainingArguments\ntraining_args = TrainingArguments(\n    output_dir=\"./results\",\n    report_to=\"wandb\",\n    run_name=\"bert-finetuning\",\n    logging_steps=100\n)"
    }
}

guardrails {
  text: "Always call wandb.finish() at end of training"
  scope: WRITE_OPS
}

guardrails {
  text: "Use WANDB_MODE=offline for unstable connections; sync later with wandb sync"
  scope: ALWAYS
}

guardrails {
  text: "Never hardcode API keys — use WANDB_API_KEY env var or wandb login"
  scope: AUTH_OPS
}

related {
  name: "evaluating-llms-harness"
  relationship: "composes_with"
  description: "Log lm-eval-harness benchmark results to W&B"
}
