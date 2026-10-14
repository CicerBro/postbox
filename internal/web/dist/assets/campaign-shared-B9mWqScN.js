import{H as e}from"./button-CAeU3HER.js";import{l as t}from"./index-BS05q6E_.js";import"./RequestError-m5dp4upE.js";var n=e(),r=`<!DOCTYPE html>
<html>
<head>
<meta charset="utf-8">
<meta name="viewport" content="width=device-width, initial-scale=1">
<style>body { font-family: Arial, sans-serif; }</style>
</head>
<body>
<p>Hello [Name,fallback=there],</p>
<p>Your newsletter goes here.</p>
<p><a href="[unsubscribe]">Unsubscribe</a> · <a href="[webversion]">View online</a></p>
</body>
</html>`,i=`w-full text-sm [&_th]:text-left [&_th]:font-medium [&_th]:p-3 [&_td]:p-3 [&_tr]:border-b`,a=e=>new Intl.NumberFormat().format(e),o=e=>e>=1048576?`${(e/1048576).toFixed(1)} MB`:`${(e/1024).toFixed(1)} KB`,s=e=>e===`sent`||e===`cancelled`,c=e=>e===`preparing`||e===`sending`||e===`paused`;function l({status:e}){let r=[`sent`,`delivered`,`completed`,`finished`].includes(e)?`success`:[`failed`,`cancelled`,`canceled`].includes(e)?`danger`:[`paused`,`unknown`,`scheduled`].includes(e)?`warning`:[`sending`,`preparing`,`claimed`].includes(e)?`info`:`neutral`;return(0,n.jsxs)(t,{variant:`outline`,className:`status-badge status-${r}`,children:[(0,n.jsx)(`span`,{className:`status-dot`,"aria-hidden":`true`}),e.charAt(0).toUpperCase()+e.slice(1)]})}function u(e){return(e??``).replace(/<head[\s\S]*?<\/head>/gi,` `).replace(/<style[\s\S]*?<\/style>/gi,` `).replace(/<script[\s\S]*?<\/script>/gi,` `).replace(/<[^>]*>/g,` `).replace(/\s+/g,` `).trim().slice(0,110)}export{s as a,i as c,r as i,o as n,c as o,u as r,a as s,l as t};