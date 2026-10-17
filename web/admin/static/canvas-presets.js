(function (global, factory) {
  'use strict'; const api = factory();
  if (typeof module === 'object' && module.exports) module.exports = api;
  else global.CanvasPresets = api;
})(typeof globalThis !== 'undefined' ? globalThis : this, function () {
  'use strict';
  const ids = Object.freeze(['ask-content','prepublish-review','social-drafts','periodic-brief','evidence-worksheet','story-video']);
  const copy = {
    en: {
      source:'Choose content', exact:'Choose the article', instructions:'Instructions', destination:'Choose a provider', agent:'Choose an agent', notes:'Review the result', schedule:'Optional daily brief', evidence:'Claim and supplied sources', story:'Your story brief', script:'Script instructions', scriptProvider:'Script provider', video:'Video production brief', videoProvider:'Video provider', videoReview:'Review before production',
      'ask-content':'Answer my question using only the connected content. Cite the supplied titles and distinguish facts from gaps. Do not invent missing information. Replace this instruction with your question.',
      'prepublish-review':'Review this article before publication. List unsupported claims, unclear passages, missing sources and accessibility improvements. Suggest revisions without changing or publishing the original.',
      'social-drafts':'Turn the selected article into three social post drafts. Preserve its facts, identify the intended platform and keep links as supplied. Return drafts for human review; do not publish.',
      'periodic-brief':'Summarize the selected recent content into a short editorial brief. Group the main developments, list source titles and note open questions. Do not claim access to information outside this context.',
      'evidence-worksheet':'Organize the supplied claim and source material into an evidence worksheet: exact claim, supplied evidence, provenance gaps, contradictions and questions for a human reviewer. This is not an independent fact-check or verification of a real event.',
      'story-video':'Draft a short video script from the connected story brief. Include scene sequence, narration and production notes. Do not invent evidence or imply real footage exists.',
      videoPrompt:'Create a video from the reviewed script connected to this node, using the configured tool and its declared capabilities. Follow the selected skill and production constraints. If no reviewed script is connected, do not produce a video.',
      stageNote:'Run the script stage first. Review its result note, then connect that note to the video provider. Select a configured video/render tool and review the outbound context before a separate video run. Saving does not generate or publish anything.',
      reviewNote:'Review the generated note and its sources before using it. The original content is unchanged.'
    },
    ar: {
      source:'اختيار المحتوى', exact:'اختيار المقال', instructions:'التعليمات', destination:'اختيار المزوّد', agent:'اختيار الوكيل', notes:'مراجعة النتيجة', schedule:'ملخص يومي اختياري', evidence:'الادعاء والمصادر المتاحة', story:'فكرة القصة', script:'تعليمات السيناريو', scriptProvider:'مزوّد السيناريو', video:'تعليمات إنتاج الفيديو', videoProvider:'مزوّد الفيديو', videoReview:'مراجعة قبل الإنتاج',
      'ask-content':'أجب عن سؤالي بالاعتماد على المحتوى المرتبط فقط. اذكر عناوين المصادر وميّز الحقائق عن المعلومات الناقصة. لا تخترع معلومات. استبدل هذه التعليمات بسؤالك.',
      'prepublish-review':'راجع المقال قبل النشر. حدّد الادعاءات غير المدعومة والفقرات الغامضة والمصادر الناقصة وتحسينات الإتاحة. اقترح تعديلات دون تغيير المقال الأصلي أو نشره.',
      'social-drafts':'حوّل المقال المحدد إلى ثلاث مسودات لمنشورات اجتماعية. احتفظ بالحقائق وحدّد المنصة المستهدفة واستخدم الروابط المتاحة فقط. أعد مسودات للمراجعة البشرية ولا تنشرها.',
      'periodic-brief':'لخّص المحتوى الحديث المحدد في موجز تحريري. اجمع التطورات الرئيسية واذكر عناوين المصادر والأسئلة المفتوحة. لا تدّعِ الاطلاع على معلومات خارج السياق المتاح.',
      'evidence-worksheet':'نظّم الادعاء والمصادر المقدمة في ورقة أدلة: نص الادعاء والأدلة المتاحة والفجوات في المصدر والتناقضات وأسئلة للمراجع البشري. هذه ليست عملية تحقق مستقلة أو إثباتًا لواقعة حقيقية.',
      'story-video':'اكتب سيناريو فيديو قصيرًا من فكرة القصة المرتبطة. ضمّن ترتيب المشاهد والتعليق الصوتي وملاحظات الإنتاج. لا تخترع أدلة ولا توحِ بوجود لقطات حقيقية.',
      videoPrompt:'أنشئ فيديو من السيناريو المُراجع المرتبط بهذه العقدة، باستخدام الأداة المُعدّة وقدراتها المعلنة. اتبع المهارة المختارة وقيود الإنتاج. إذا لم يوجد سيناريو مُراجع فلا تنتج فيديو.',
      stageNote:'شغّل مرحلة السيناريو أولًا. راجع ملاحظة النتيجة ثم وصّلها بمزوّد الفيديو. اختر أداة فيديو أو رندر مُعدّة وراجع السياق المرسل قبل تشغيل الفيديو منفصلًا. الحفظ لا يُنتج ولا ينشر شيئًا.',
      reviewNote:'راجع الملاحظة الناتجة ومصادرها قبل استخدامها. المحتوى الأصلي لا يتغير.'
    }
  };
  function build(id, options = {}, uuid = () => crypto.randomUUID()) {
    if (!ids.includes(id)) throw new Error('unknown preset');
    const targetKind = options.targetKind || 'provider';
    if (!['provider','agent'].includes(targetKind)) throw new Error('invalid target');
    const c = copy[options.locale === 'ar' ? 'ar' : 'en'], cards = [], edges = [];
    const node = (kind,title,x,y,fields={}) => { const card={id:uuid(),kind,title,body:'',x,y,...fields}; cards.push(card); return card; };
    const target = (title,x,y,kind='text') => node(targetKind,title,x,y,targetKind==='agent'?{agent_id:''}:{provider_id:'',generation_kind:kind});
    const connect = (from,to) => edges.push({from:from.id,to:to.id});
    let source;
    if (id === 'evidence-worksheet' || id === 'story-video') source=node('note',id==='story-video'?c.story:c.evidence,24,24);
    else if (['prepublish-review','social-drafts'].includes(id)) source=node('content',c.exact,24,24,{content_id:''});
    else source=node('source',c.source,24,24,{source_type:'',source_limit:3});
    const prompt=node('prompt',id==='story-video'?c.script:c.instructions,24,430,{body:c[id]});
    const destination=target(id==='story-video'?c.scriptProvider:(targetKind==='agent'?c.agent:c.destination),420,200);
    connect(source,destination);connect(prompt,destination);
    if(id==='periodic-brief'){const schedule=node('schedule',c.schedule,816,24,{interval:'daily'});connect(schedule,destination);}
    if(id==='story-video'){
      const videoPrompt=node('prompt',c.video,816,24,{body:c.videoPrompt});
      const video=node('provider',c.videoProvider,1212,200,{provider_id:'',generation_kind:'video'});connect(videoPrompt,video);
      node('note',c.videoReview,816,430,{body:c.stageNote});
    } else node('note',c.notes,816,430,{body:c.reviewNote});
    return {cards,edges};
  }
  return Object.freeze({ids,build});
});
