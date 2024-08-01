# 🦒💨 Giraffe Spam Crusher: Advanced Spam Mitigation Bot for Telegram

## Overview

Giraffe Spam Crusher is a sophisticated Telegram bot designed for efficient spam detection and mitigation in channels and groups. It leverages state-of-the-art artificial intelligence to identify and neutralize spam content with high accuracy.

## Key Features

- Utilizes advanced natural language processing models from OpenAI or Anthropic for message analysis
- Implements user tracking mechanisms to optimize processing efficiency
- Provides configurable spam sensitivity thresholds
- Supports retrospective analysis of message history in existing groups
- Offers channel-specific targeting capabilities
- Generates comprehensive operational statistics

## Operational Workflow
 
1. **New User Monitoring:** Focuses analysis on messages from recent group joiners
2. **AI-Powered Analysis:** Employs machine learning models for precise spam classification
3. **Flexible Configuration:** Allows fine-tuning of detection parameters
4. **Proactive Mitigation:** Executes automated spam removal or flagging based on analysis results
5. **Historical Processing:** Capable of analyzing pre-existing group messages
6. **Selective Deployment:** Operates exclusively within designated channels
   
## Installation Guide

1. Clone the repository:
   ```
   git clone https://github.com/ailabhub/giraffe-spam-crasher.git
   cd giraffe-spam-crasher
   ```

2. Configure the bot by creating a `.env` file with the following parameters:
   ```
   TELEGRAM_BOT_TOKEN=your_bot_token
   OPENAI_API_KEY=your_openai_key
   ANTHROPIC_API_KEY=your_anthropic_key
   HISTORY=/root/result.json
   PROMPT=/root/prompt.txt
   MODEL=claude-3-5-sonnet-20240620
   PROVIDER=anthropic
   SPAM_THRESHOLD=0.6
   NEW_USER_THRESHOLD=1
   WHITELIST_CHANNELS=
   LOG_LEVEL=info
   ```

 3. On first launch, export the chat history to a file named results.json.
   
![alt text](image.png)
  

## Deployment Instructions

Ensure Docker and Docker Compose are installed on your system, then:

1. Initialize the bot:
   ```
   docker-compose up -d
   ```

2. Monitor bot operations:
   ```
   docker-compose logs -f bot
   ```

3. Terminate the bot:
   ```
   docker-compose down
   ```

## Advanced Configuration

The `.env` file supports the following customizable parameters:

- `HISTORY`: Specifies the message history file location
- `PROMPT`: Defines AI instruction set
- `MODEL`: Selects the AI model for analysis
- `PROVIDER`: Toggles between OpenAI and Anthropic services
- `SPAM_THRESHOLD`: Adjusts spam detection sensitivity (range: 0-1)
- `NEW_USER_THRESHOLD`: Sets the message count threshold for "new user" classification
- `WHITELIST_CHANNELS`: Enumerates permitted channels
- `LOG_LEVEL`: Controls logging verbosity

## Architectural Overview

Giraffe Spam Crusher is composed of three primary modules:
- `ai`: Handles AI model interactions
- `bot`: Manages Telegram API communications
- `history`: Facilitates message data persistence

## Contribution Guidelines

We welcome contributions to enhance Giraffe Spam Crusher. Please submit issues or pull requests via GitHub for proposed improvements or bug fixes.

## License Information

Giraffe Spam Crusher is distributed under the MIT License. This permits free use, modification, and distribution of the bot, provided that the original copyright notice and license text are preserved within the codebase.

```
MIT License

Copyright (c) 2024 AILabHub

[Full license text]
```