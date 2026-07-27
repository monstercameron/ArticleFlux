package lexicon

import "github.com/monstercameron/ArticleFlux/internal/classify"

// ai is the AI & Machine Learning category (plan.md §27.3d #2).
//
// Model-family names double as ordinary words often enough that most of them
// need a guard: `llama` is an animal, `gemini` is a zodiac sign and a NASA
// programme, `claude` is a person's name, `mistral` is a wind. `agent`
// carries the same problem in the other direction — its dominant sense in a
// general news feed is a real-estate or travel agent, not an autonomous one.
func ai() classify.Label {
	aiCompanions := []string{
		"ai", "llm", "model", "openai", "anthropic", "chatbot", "machine learning",
		"neural network", "artificial intelligence", "training data", "inference",
	}
	return classify.Label{
		Slug: "ai",
		Name: "AI & Machine Learning",
		Terms: []classify.Term{
			{Text: "large language model", Weight: 2.6},
			{Text: "chatgpt", Weight: 2.2},
			{Text: "generative ai", Weight: 2.2},
			{Text: "hallucination", Weight: 2.2},
			{Text: "stable diffusion", Weight: 2.0},
			{Text: "mixture of experts", Weight: 2.0},
			{Text: "foundation model", Weight: 2.0},
			{Text: "fine-tune", Weight: 2.0},
			{Text: "agi", Weight: 2.0},
			{Text: "gpt", Weight: 1.9},
			{Text: "chain of thought", Weight: 1.8},
			{Text: "diffusion model", Weight: 1.8},
			{Text: "gpu training", Weight: 1.8},
			{Text: "midjourney", Weight: 1.8},
			{Text: "text to image", Weight: 1.8},
			{Text: "neural network", Weight: 1.8},
			{Text: "pretraining", Weight: 1.8},
			{Text: "backpropagation", Weight: 1.8},
			{Text: "artificial intelligence", Weight: 1.8},
			{Text: "reinforcement learning", Weight: 1.8},
			{Text: "autonomous agent", Weight: 1.8},
			{Text: "open weights", Weight: 1.8},
			{Text: "superintelligence", Weight: 1.7},
			{Text: "embedding", Weight: 1.6},
			{Text: "machine learning", Weight: 1.6},
			{Text: "multimodal", Weight: 1.6},
			{Text: "prompt engineering", Weight: 1.6},
			{Text: "gradient descent", Weight: 1.6},
			{Text: "computer vision", Weight: 1.6},
			{Text: "natural language processing", Weight: 1.6},
			{Text: "hyperparameter", Weight: 1.6},
			{Text: "vector database", Weight: 1.6},
			{Text: "model weights", Weight: 1.6},
			{Text: "multi-agent", Weight: 1.6},
			{Text: "hugging face", Weight: 1.6},
			{Text: "pytorch", Weight: 1.6},
			{Text: "tensorflow", Weight: 1.6},
			{Text: "openai", Weight: 1.5},
			{Text: "transformer model", Weight: 1.5},
			{Text: "synthetic data", Weight: 1.4},
			{Text: "overfitting", Weight: 1.4},
			{Text: "semantic search", Weight: 1.4},
			{Text: "turing test", Weight: 1.4},
			{Text: "anthropic", Weight: 1.4},
			{Text: "deepmind", Weight: 1.4},
			{Text: "copilot", Weight: 1.3, Requires: []string{
				"ai", "code", "github", "microsoft", "coding assistant", "llm",
			}},
			{Text: "rag pipeline", Weight: 1.8},
			{Text: "retrieval augmented", Weight: 1.8},
			{Text: "alignment", Weight: 1.3, Requires: []string{
				"ai", "llm", "model", "safety", "rlhf", "openai", "anthropic", "superintelligence",
			}},
			{Text: "gemini", Weight: 1.3, Requires: []string{
				"google", "ai", "llm", "model", "chatbot", "deepmind",
			}},
			{Text: "claude", Weight: 1.3, Requires: []string{
				"anthropic", "ai", "llm", "model", "chatbot",
			}},
			{Text: "llama", Weight: 1.3, Requires: []string{
				"meta", "ai", "llm", "model", "open weights", "fine-tune",
			}},
			{Text: "mistral", Weight: 1.3, Requires: []string{
				"ai", "llm", "model", "paris", "open weights",
			}},
			{Text: "jax", Weight: 1.0},
			{Text: "orchestration", Weight: 0.9},
			{Text: "dataset", Weight: 0.9},
			{Text: "robotics", Weight: 0.9},
			{Text: "benchmark", Weight: 0.8},
			{Text: "tensor", Weight: 0.8},
			{Text: "prompt", Weight: 0.7},
			{Text: "gpu", Weight: 0.6},
			{Text: "nvidia", Weight: 0.6},
			{Text: "token", Weight: 0.5, Requires: []string{
				"llm", "model", "context window", "inference", "prompt", "tokenizer",
			}},
			{Text: "agent", Weight: 0.9, Requires: aiCompanions},
			{Text: "notebooklm", Weight: 1.6},
			{Text: "chatbot", Weight: 1.0},
			{Text: "llm", Weight: 1.3},
			{Text: "llms", Weight: 1.3},
			{Text: "deepseek", Weight: 1.5},
			{Text: "grok", Weight: 1.4},
			{Text: "qwen", Weight: 1.4},
			{Text: "kimi", Weight: 1.3},
			{Text: "perplexity ai", Weight: 1.4},
		},
		Exclude: []classify.Term{
			{Text: "real estate agent", Weight: 3.0},
			{Text: "travel agent", Weight: 3.0},
			{Text: "insurance agent", Weight: 3.0},
			{Text: "secret agent", Weight: 2.0},
			{Text: "border patrol agent", Weight: 3.0},
			{Text: "pack llama", Weight: 3.0},
			{Text: "llama farm", Weight: 3.0},
			{Text: "wheel alignment", Weight: 2.5},
			{Text: "political alignment", Weight: 2.0},
		},
		MinScore: 0,
		Prompt: "Assign for AI/ML research, models, training and tooling. Not for a company's " +
			"unrelated business news, and not for every mention of automation — a self-driving " +
			"car belongs to Transport, a trading algorithm to Finance.",
	}
}
