name: threadser
description: Post and interact with Threads (Meta social media). Use threads_post to publish text or images. Use threads_get_replies to read responses.
triggers:
  - post to threads
  - publish on threads
  - thread post
  - threads update
  - social media post
  - share on threads
  - tweet on threads
instructions: |
  You have access to Threads (Meta's social platform) via two tools:

  threads_post — publish a text post. Required: text (max 500 chars). Optional: image_url, reply_to_id.
  threads_get_replies — read replies to your posts. Required: thread_id.

  When the user asks to post, share, or publish to Threads, CALL threads_post directly.
  Do not ask for confirmation. Do not explain what you'll post — just call the tool.
  The tool returns the post ID on success.
