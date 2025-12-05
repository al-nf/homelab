import discord
import requests
import json

with open("secrets.json") as f:
    secrets = json.load(f)

TOKEN = secrets["discord_token"]

intents = discord.Intents.default()
intents.message_content = True

client = discord.Client(intents=intents)

@client.event
async def on_ready():
    print(f"Bot online as {client.user}")

@client.event
async def on_message(message):
    # ignore self
    if message.author == client.user:
        return

    if isinstance(message.channel, discord.DMChannel):
        # send a post
        try:
            requests.post("http://localhost:6767/discord")
        except Exception as e:
            print("Failed to POST:", e)

        await message.channel.send("ping sent.")
        me = await client.fetch_user(723957574743097415)
        await me.send(f"**{message.author}** wants your attention!")


client.run(TOKEN)

