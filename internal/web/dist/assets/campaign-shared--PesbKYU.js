import"./RequestError-DCSfkLb3.js";import"./status-badge-B6gSTcYL.js";var e=`<!DOCTYPE html>
<html>
<head>
<meta charset="utf-8">
<meta name="viewport" content="width=device-width, initial-scale=1">
<style>body { font-family: Arial, sans-serif; }</style>
</head>
<body>
<p>Hello {{ name | "there" }},</p>
<p>Your newsletter goes here.</p>
<p><a href="{{ unsubscribe }}">Unsubscribe</a> · <a href="{{ webversion }}">View online</a></p>
</body>
</html>`,t=`w-full text-sm [&_th]:text-left [&_th]:font-medium [&_th]:p-3 [&_td]:p-3 [&_tr]:border-b`,n=e=>new Intl.NumberFormat().format(e),r=e=>e>=1048576?`${(e/1048576).toFixed(1)} MB`:`${(e/1024).toFixed(1)} KB`,i=e=>e===`sent`||e===`cancelled`,a=e=>e===`preparing`||e===`sending`||e===`paused`;function o(e){return(e??``).replace(/<head[\s\S]*?<\/head>/gi,` `).replace(/<style[\s\S]*?<\/style>/gi,` `).replace(/<script[\s\S]*?<\/script>/gi,` `).replace(/<[^>]*>/g,` `).replace(/\s+/g,` `).trim().slice(0,110)}export{a,i,o as n,n as o,e as r,t as s,r as t};