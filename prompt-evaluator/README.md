# Spam Classification CLI

This Command Line Interface (CLI) tool uses AI to classify text as spam or not spam. It supports both OpenAI and Anthropic API providers and can process multiple entries from a CSV file.

## Features

- Supports OpenAI and Anthropic AI providers
- Processes CSV input files
- Customizable prompts
- Rate limiting for API requests
- Concurrent processing for faster results
- JSON output for easy integration with other tools

## Prerequisites

- Go 1.16 or later
- OpenAI API key (for OpenAI provider) or Anthropic API key (for Anthropic provider)


## Usage

The basic syntax for running the CLI is:

```
go run main.go -input <input_file.csv> -prompt <prompt_file.txt> -provider <provider> -model <model> -ratelimit <rate>
```

### Parameters

- `-input`: Path to the input CSV file (required)
- `-prompt`: Path to the prompt text file (optional)
- `-provider`: API provider to use, either "openai" or "anthropic" (default: "openai")
- `-model`: Model to use (e.g., "gpt-4" for OpenAI, "claude-2" for Anthropic)
- `-ratelimit`: Rate limit for API requests in requests per second (default: 0, meaning no limit)

### Environment Variables

- `OPENAI_API_KEY`: Your OpenAI API key (required when using OpenAI provider)
- `ANTHROPIC_API_KEY`: Your Anthropic API key (required when using Anthropic provider)

## Examples

1. Using OpenAI with GPT-4:
   ```
   export OPENAI_API_KEY=your_openai_api_key_here
   go run main.go -input data.csv -prompt prompt.txt -provider openai -model gpt-4 -ratelimit 0.5
   ```

2. Using Anthropic with Claude-2:
   ```
   export ANTHROPIC_API_KEY=your_anthropic_api_key_here
   go run main.go -input data.csv -prompt prompt.txt -provider anthropic -model claude-2 -ratelimit 1
   ```

3. Using OpenAI with GPT-3.5-turbo and no rate limit:
   ```
   export OPENAI_API_KEY=your_openai_api_key_here
   go run main.go -input data.csv -provider openai -model gpt-3.5-turbo
   ```

4. Using default settings (OpenAI provider) with a custom prompt:
   ```
   export OPENAI_API_KEY=your_openai_api_key_here
   go run main.go -input data.csv -prompt custom_prompt.txt
   ```

5. Using Anthropic with a high rate limit for faster processing:
   ```
   export ANTHROPIC_API_KEY=your_anthropic_api_key_here
   go run main.go -input large_data.csv -provider anthropic -model claude-2 -ratelimit 10
   ```

6.  Using CSV file with quotes:   
   ```
   go run main.go -input=quoted_training_good.csv -prompt=prompt.txt -model=claude-sonnet-4-6 -provider=anthropic -ratelimit=0.5
   ```

   ```
   go run main.go -input=quoted_training_good.csv -prompt=prompt.txt -model=gpt-4o-mini-2024-07-18  -provider=openai -ratelimit 1
   ```

## Output

The tool will output a JSON file with detailed logs and print a summary of the spam classification results to the console.

## License

This project is licensed under the MIT License - see the [LICENSE](LICENSE) file for details.
